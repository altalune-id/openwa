package alert

import (
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/sealer"
)

type optOut struct {
	OrgID     uuid.UUID `json:"o"`
	ProjectID uuid.UUID `json:"p"`
	Email     string    `json:"e"`
}

//nolint:gochecknoglobals // Immutable seal purpose.
var optOutAAD = []byte("alert-optout:v1")

func sealOptOut(sl sealer.Sealer, o optOut) (string, error) {
	plain, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	ct, err := sl.Seal(plain, optOutAAD)
	if err != nil {
		return "", err
	}
	return encodeToken(ct), nil
}

func encodeToken(ct []byte) string { return base64.RawURLEncoding.EncodeToString(ct) }

func openOptOut(sl sealer.Sealer, token string) (optOut, error) {
	ct, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(ct) == 0 {
		return optOut{}, &InvalidUnsubscribeTokenError{}
	}
	plain, err := sl.Open(ct, optOutAAD)
	if err != nil {
		return optOut{}, &InvalidUnsubscribeTokenError{}
	}
	var o optOut
	if err := json.Unmarshal(plain, &o); err != nil || o.OrgID == uuid.Nil || o.ProjectID == uuid.Nil || o.Email == "" {
		return optOut{}, &InvalidUnsubscribeTokenError{}
	}
	return o, nil
}
