package alert

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/mailer"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/alert")

const countRetention = 48 * time.Hour

// Sessions finds and claims the numbers whose outage an alert reports.
type Sessions interface {
	// ProjectsNeedingAlert lists the org's projects with a number eligible for an alert.
	ProjectsNeedingAlert(ctx context.Context, grace, cooldown time.Duration) ([]uuid.UUID, error)
	// ClaimOutages marks the eligible numbers of the project on ctx notified and returns them.
	ClaimOutages(ctx context.Context, grace, cooldown time.Duration) ([]Outage, error)
}

// Members lists an org's members.
type Members interface {
	Members(ctx context.Context, orgID uuid.UUID) ([]Member, error)
}

// Projects names a project.
type Projects interface {
	Describe(ctx context.Context, projectID uuid.UUID) (Project, error)
}

// Queue sends mail, joining the transaction on ctx.
type Queue interface {
	Enqueue(ctx context.Context, m mailer.Message) (uuid.UUID, error)
}

// Translations resolves the email copy for a locale.
type Translations interface {
	For(l i18n.Locale) *i18n.Translator
	Parse(tag string) (i18n.Locale, error)
	Default() i18n.Locale
}

// Options are the operator settings alerts run under.
type Options struct {
	// MailEnabled is true when a mail driver delivers; nothing is queued otherwise.
	MailEnabled bool
	// Enabled is the server-wide switch for disconnect emails.
	Enabled     bool
	Grace       time.Duration
	Cooldown    time.Duration
	DailyOrgCap int
	// LinkBase is the console's absolute URL prefix, without a trailing slash.
	LinkBase string
	// Brand is the product name the email header shows.
	Brand string
}

// Deps are the collaborators a Service needs.
type Deps struct {
	Store        Store
	Sessions     Sessions
	Members      Members
	Projects     Projects
	Queue        Queue
	UnitOfWork   tenant.UnitOfWork
	Sealer       sealer.Sealer
	Translations Translations
	Log          *slog.Logger
	Unexpected   apperror.UnexpectedFunc
}

// Service owns project alert settings, test emails, opt-outs and the disconnect job.
type Service struct {
	d    Deps
	opts Options
	log  *slog.Logger
}

// NewService binds the service to its dependencies.
func NewService(d Deps, opts Options) *Service {
	return &Service{d: d, opts: opts, log: d.Log.With("module", "alert")}
}

// MailReady reports whether alerts can be delivered: a delivering mail driver and an absolute link base.
func (s *Service) MailReady() bool { return s.opts.MailEnabled }

// Settings returns the alert settings of the project on ctx.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	ctx, span := tracer.Start(ctx, "alert.Settings")
	defer span.End()
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return Settings{}, s.d.Unexpected(ctx, "alert.Settings", err)
	}
	return st, nil
}

// Unsubscribed lists the addresses that left the alerts of the project on ctx.
func (s *Service) Unsubscribed(ctx context.Context) ([]string, error) {
	ctx, span := tracer.Start(ctx, "alert.Unsubscribed")
	defer span.End()
	out, err := s.d.Store.OptOuts(ctx)
	if err != nil {
		return nil, s.d.Unexpected(ctx, "alert.Unsubscribed", err)
	}
	return out, nil
}

// SaveSettings parses rawRecipients and stores the settings of the project on ctx.
func (s *Service) SaveSettings(ctx context.Context, enabled bool, rawRecipients string) (Settings, error) {
	ctx, span := tracer.Start(ctx, "alert.SaveSettings")
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return Settings{}, err
	}
	recipients, err := ParseRecipients(rawRecipients)
	if err != nil {
		return Settings{}, err
	}
	st := Settings{OrgID: tc.OrgID, ProjectID: tc.ProjectID, Enabled: enabled, Recipients: recipients, UpdatedAt: time.Now().UTC()}
	if err := s.d.Store.SaveSettings(ctx, &st); err != nil {
		return Settings{}, s.d.Unexpected(ctx, "alert.SaveSettings", err)
	}
	return st, nil
}

// SendTest queues one test email to each address of the project on ctx and returns them.
func (s *Service) SendTest(ctx context.Context) ([]string, error) {
	ctx, span := tracer.Start(ctx, "alert.SendTest")
	defer span.End()
	switch {
	case !s.opts.MailEnabled:
		return nil, &MailUnavailableError{}
	case !s.opts.Enabled:
		return nil, &DisabledError{}
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return nil, s.d.Unexpected(ctx, "alert.SendTest: settings", err)
	}
	recipients, err := s.recipients(ctx, tc.OrgID, st)
	if err != nil {
		return nil, s.d.Unexpected(ctx, "alert.SendTest: recipients", err)
	}
	if len(recipients) == 0 {
		return nil, &NoRecipientsError{}
	}
	project, err := s.d.Projects.Describe(ctx, tc.ProjectID)
	if err != nil {
		return nil, s.d.Unexpected(ctx, "alert.SendTest: project", err)
	}
	err = s.d.UnitOfWork(ctx, func(ctx context.Context) error {
		ok, err := s.underCap(ctx, len(recipients))
		if err != nil {
			return err
		}
		if !ok {
			return &TestLimitError{}
		}
		ok, err = s.d.Store.TakeTestSlot(ctx, TestsPerHour)
		if err != nil {
			return err
		}
		if !ok {
			return &TestLimitError{}
		}
		return s.enqueue(ctx, recipients, func(tr *i18n.Translator, r Recipient) (mailer.Message, error) {
			return s.testEmail(tr, r, tc, project)
		})
	})
	switch {
	case IsTestLimitError(err):
		return nil, err
	case err != nil:
		return nil, s.d.Unexpected(ctx, "alert.SendTest", err)
	}
	to := make([]string, len(recipients))
	for i, r := range recipients {
		to[i] = r.Email
	}
	return to, nil
}

func (s *Service) underCap(ctx context.Context, n int) (bool, error) {
	sent, err := s.d.Store.SentToday(ctx)
	if err != nil {
		return false, err
	}
	return sent+n <= s.opts.DailyOrgCap, nil
}

// RunDisconnect emails the outages of every project of the org on ctx; the scheduler runs it once per org.
func (s *Service) RunDisconnect(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "alert.RunDisconnect")
	defer span.End()
	if !s.opts.MailEnabled || !s.opts.Enabled {
		return nil
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	if err := s.d.Store.PruneCounts(ctx, time.Now().UTC().Add(-countRetention)); err != nil {
		return fmt.Errorf("alert.RunDisconnect: prune: %w", err)
	}
	projects, err := s.d.Sessions.ProjectsNeedingAlert(ctx, s.opts.Grace, s.opts.Cooldown)
	if err != nil {
		return fmt.Errorf("alert.RunDisconnect: candidates: %w", err)
	}
	var errs []error
	for _, p := range projects {
		if err := s.notifyProject(tenant.WithProject(ctx, p), tc.OrgID, p); err != nil {
			errs = append(errs, fmt.Errorf("alert.RunDisconnect: project %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) notifyProject(ctx context.Context, orgID, projectID uuid.UUID) error {
	st, err := s.d.Store.Settings(ctx)
	if err != nil {
		return err
	}
	if !st.Enabled {
		return nil
	}
	recipients, err := s.recipients(ctx, orgID, st)
	if err != nil {
		return err
	}
	ok, err := s.underCap(ctx, len(recipients))
	if err != nil {
		return err
	}
	if !ok {
		s.log.DebugContext(ctx, "alert: daily cap reached, outage left for later", slog.String("project_id", projectID.String()))
		return nil
	}
	tc := tenant.Context{OrgID: orgID, ProjectID: projectID}
	return s.d.UnitOfWork(ctx, func(ctx context.Context) error {
		outages, err := s.d.Sessions.ClaimOutages(ctx, s.opts.Grace, s.opts.Cooldown)
		if err != nil || len(outages) == 0 {
			return err
		}
		project, err := s.d.Projects.Describe(ctx, projectID)
		if err != nil {
			return err
		}
		return s.enqueue(ctx, recipients, func(tr *i18n.Translator, r Recipient) (mailer.Message, error) {
			return s.disconnectEmail(tr, r, tc, project, outages)
		})
	})
}

func (s *Service) enqueue(ctx context.Context, recipients []Recipient, build func(*i18n.Translator, Recipient) (mailer.Message, error)) error {
	for _, r := range recipients {
		m, err := build(s.translator(r.Locale), r)
		if err != nil {
			return err
		}
		if _, err := s.d.Queue.Enqueue(ctx, m); err != nil {
			return err
		}
	}
	return s.d.Store.AddSent(ctx, len(recipients))
}

func (s *Service) translator(tag string) *i18n.Translator {
	loc, err := s.d.Translations.Parse(tag)
	if err != nil || tag == "" {
		loc = s.d.Translations.Default()
	}
	return s.d.Translations.For(loc)
}

// NOTE: no saved address means the org's owners and admins; a typed address that is a member's takes the member's locale.
func (s *Service) recipients(ctx context.Context, orgID uuid.UUID, st Settings) ([]Recipient, error) {
	members, err := s.d.Members.Members(ctx, orgID)
	if err != nil {
		return nil, err
	}
	optOuts, err := s.d.Store.OptOuts(ctx)
	if err != nil {
		return nil, err
	}
	locales := make(map[string]string, len(members))
	var managers []string
	for _, m := range members {
		locales[m.Email] = m.Locale
		if m.Manager {
			managers = append(managers, m.Email)
		}
	}
	addrs := st.Recipients
	if len(addrs) == 0 {
		addrs = managers
	}
	out := make([]Recipient, 0, len(addrs))
	for _, a := range addrs {
		if !slices.Contains(optOuts, a) {
			out = append(out, Recipient{Email: a, Locale: locales[a]})
		}
	}
	return out, nil
}

// Target is the address and project an unsubscribe link names.
type Target struct {
	Email   string
	Project string
}

// UnsubscribeTarget opens token for the confirm page without recording anything.
func (s *Service) UnsubscribeTarget(ctx context.Context, token string) (Target, error) {
	ctx, span := tracer.Start(ctx, "alert.UnsubscribeTarget")
	defer span.End()
	o, ctx, err := s.openToken(ctx, token)
	if err != nil {
		return Target{}, err
	}
	return s.target(ctx, o)
}

// Unsubscribe records the opt-out token names; it is idempotent.
func (s *Service) Unsubscribe(ctx context.Context, token string) (Target, error) {
	ctx, span := tracer.Start(ctx, "alert.Unsubscribe")
	defer span.End()
	o, ctx, err := s.openToken(ctx, token)
	if err != nil {
		return Target{}, err
	}
	if err := s.d.Store.AddOptOut(ctx, o.Email); err != nil {
		return Target{}, s.d.Unexpected(ctx, "alert.Unsubscribe", err)
	}
	return s.target(ctx, o)
}

// SECURITY: the sealed token is the only credential, so the tenant scope comes from it alone.
func (s *Service) openToken(ctx context.Context, token string) (optOut, context.Context, error) {
	o, err := openOptOut(s.d.Sealer, token)
	if err != nil {
		return optOut{}, ctx, err
	}
	return o, tenant.Into(ctx, tenant.Context{OrgID: o.OrgID, ProjectID: o.ProjectID}), nil
}

func (s *Service) target(ctx context.Context, o optOut) (Target, error) {
	p, err := s.d.Projects.Describe(ctx, o.ProjectID)
	if err != nil {
		return Target{}, s.d.Unexpected(ctx, "alert.Unsubscribe: project", err)
	}
	return Target{Email: o.Email, Project: p.Name}, nil
}
