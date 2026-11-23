package server

import (
	"net/http"
)

// bundleProfileUse is the compact profile shape a usage panel needs. It
// deliberately omits the profile config: a usage row names the dependency,
// it does not re-publish the profile body.
type bundleProfileUse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	NodeType string `json:"nodeType"`
	Version  int    `json:"version"`
}

// bundleProfilesFor lists the visible profiles whose current bundle
// references the asset. Private profiles owned by someone else are omitted,
// matching the profile read rule.
func (s *api) bundleProfilesFor(r *http.Request, kind, assetID string) ([]bundleProfileUse, error) {
	profiles, err := s.deps.Store.ProfilesReferencingAsset(kind, assetID)
	if err != nil {
		return nil, err
	}
	viewer := viewerFrom(r)
	out := make([]bundleProfileUse, 0, len(profiles))
	for i := range profiles {
		profile := &profiles[i]
		if !viewer.profileVisibleTo(profile) {
			continue
		}
		out = append(out, bundleProfileUse{
			ID: profile.ID, Name: profile.Name,
			NodeType: profile.NodeType, Version: profile.Version,
		})
	}
	return out, nil
}

func bundleProfilesJSON(profiles []bundleProfileUse) []bundleProfileUse {
	if profiles == nil {
		return []bundleProfileUse{}
	}
	return profiles
}
