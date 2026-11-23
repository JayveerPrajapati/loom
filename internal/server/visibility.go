package server

import (
	"net/http"

	"loom/internal/auth"
	"loom/internal/store"
)

// Asset visibility (Profile-Centric UX Phase 1, D-P0a): private
// profiles/mappings/schemas are visible and usable only by their creator
// (stable user id) and admins; everyone else gets 404s and named
// validation errors at draft save / publish. Creator-less assets
// (CLI store-direct import) behave as shared.

type viewer struct {
	userID  string
	isAdmin bool
}

func viewerFrom(r *http.Request) viewer {
	principal, ok := principalFrom(r.Context())
	if !ok {
		return viewer{}
	}
	id := principal.UserID
	if id == "" {
		id = principal.ID
	}
	return viewer{userID: id, isAdmin: principal.Role == auth.RoleAdmin}
}

// canSee reports whether the viewer may read and use an asset with the
// given governance columns. Anything that is not explicitly private reads
// as shared (legacy rows, column defaults).
func (v viewer) canSee(visibility, creatorID string) bool {
	if visibility != store.VisibilityPrivate {
		return true
	}
	return v.isAdmin || (v.userID != "" && v.userID == creatorID)
}

// profileVisibleTo applies canSee to a profile.
func (v viewer) profileVisibleTo(p *store.ConfigProfile) bool {
	return v.canSee(p.Visibility, p.CreatorID)
}

// mappingVisibleTo applies canSee to a mapping.
func (v viewer) mappingVisibleTo(m *store.Mapping) bool {
	return v.canSee(m.Visibility, m.CreatorID)
}

// schemaVisibleTo applies canSee to a data schema.
func (v viewer) schemaVisibleTo(s *store.DataSchema) bool {
	return v.canSee(s.Visibility, s.CreatorID)
}

// scriptVisibleTo applies canSee to a script (Phase 4).
func (v viewer) scriptVisibleTo(s *store.Script) bool {
	return v.canSee(s.Visibility, s.CreatorID)
}

// canGovern reports whether the viewer may promote or demote an asset:
// admins always, the creator for their own assets.
func (v viewer) canGovern(creatorID string) bool {
	return v.isAdmin || (v.userID != "" && v.userID == creatorID)
}
