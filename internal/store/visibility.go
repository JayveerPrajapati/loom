package store

import "errors"

// Asset visibility (Profile-Centric UX Phase 1, D-P0a/D-P0b/D-P0c):
// private assets are visible and usable only by their creator (stable
// user id) and admins; shared assets by everyone. The columns live on the
// entity tables — never inside append-only version docs — so sharing
// never forks a version. Existing rows upgraded to 'shared' with an
// empty creator (D-P0c); assets with no creator behave as shared.

const (
	VisibilityPrivate = "private"
	VisibilityShared  = "shared"
)

var ErrInvalidVisibility = errors.New("visibility must be private or shared")

// ValidVisibility reports whether v names an allowed visibility value.
func ValidVisibility(v string) bool {
	return v == VisibilityPrivate || v == VisibilityShared
}

// normalizeVisibility maps the stored value to a visibility decision:
// anything other than "private" reads as shared (the column default and
// the empty creator both cover legacy rows).
func normalizeVisibility(v string) string {
	if v == VisibilityPrivate {
		return VisibilityPrivate
	}
	return VisibilityShared
}

// SetConfigProfileVisibility promotes or demotes a profile without
// creating a version (D-P0b: governance is not content).
func (s *Store) SetConfigProfileVisibility(id, visibility string) error {
	if !ValidVisibility(visibility) {
		return ErrInvalidVisibility
	}
	return s.setAssetVisibility("config_profiles", ErrConfigProfileNotFound, id, visibility)
}

// SetMappingVisibility promotes or demotes a mapping without creating a
// version.
func (s *Store) SetMappingVisibility(id, visibility string) error {
	if !ValidVisibility(visibility) {
		return ErrInvalidVisibility
	}
	return s.setAssetVisibility("mappings", ErrMappingNotFound, id, visibility)
}

// SetDataSchemaVisibility promotes or demotes a schema without creating
// a version.
func (s *Store) SetDataSchemaVisibility(id, visibility string) error {
	if !ValidVisibility(visibility) {
		return ErrInvalidVisibility
	}
	return s.setAssetVisibility("data_schemas", ErrDataSchemaNotFound, id, visibility)
}

// SetScriptVisibility promotes or demotes a script without creating a
// version (Phase 4).
func (s *Store) SetScriptVisibility(id, visibility string) error {
	if !ValidVisibility(visibility) {
		return ErrInvalidVisibility
	}
	return s.setAssetVisibility("scripts", ErrScriptNotFound, id, visibility)
}

func (s *Store) setAssetVisibility(table string, notFound error, id, visibility string) error {
	res, err := s.db.Exec(`UPDATE `+table+` SET visibility = ? WHERE id = ?`, visibility, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return notFound
	}
	return nil
}
