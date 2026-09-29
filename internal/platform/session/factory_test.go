package session

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/sealer"
)

func TestNewStore_ReturnsThePostgresStore(t *testing.T) {
	conn := &sql.DB{}
	got := NewStore(db.DBConfig{TablePrefix: "openwa_"}, db.Pool{W: conn, R: conn}, sealer.Disabled(), nil)
	assert.IsType(t, &pgStore{}, got)
}

func TestNewStore_NilUnexpectedIsTolerated(t *testing.T) {
	conn := &sql.DB{}
	s, ok := NewStore(
		db.DBConfig{TablePrefix: "openwa_"},
		db.Pool{W: conn, R: conn}, sealer.Disabled(), nil).(*pgStore)
	require.True(t, ok)
	require.NotNil(t, s.unexpected, "a nil reporter must be replaced, not stored and later dereferenced")
	assert.Nil(t, s.unexpected(t.Context(), "msg", nil))
}

func TestNewStore_PostgresReadsThroughTheWriter(t *testing.T) {
	w, r := &sql.DB{}, &sql.DB{}
	s, ok := NewStore(
		db.DBConfig{TablePrefix: "openwa_"},
		db.Pool{W: w, R: r}, sealer.Disabled(), nil).(*pgStore)
	require.True(t, ok)
	assert.Same(t, w, s.db, "a lagging replica would miss the row the post-login redirect reads")
}
