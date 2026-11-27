package boot

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"altalune.id/openwa/internal/invite"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/user"
)

func TestOrgStoreForInvite_MissingMembershipIsTyped(t *testing.T) {
	_, err := orgStoreForInvite{store: fakes.NewOrg()}.MembershipOf(t.Context(), uuid.New(), uuid.New())
	assert.True(t, invite.IsMembershipMissingError(err), "got %v", err)
}

func TestOrgStoreForOnboard_MissingMembershipIsTyped(t *testing.T) {
	_, err := orgStoreForOnboard{store: fakes.NewOrg()}.MembershipOf(t.Context(), uuid.New(), uuid.New())
	assert.True(t, user.IsMembershipMissingError(err), "got %v", err)
}
