package store

import (
	"strconv"
	"sync"
)

// lookupEntry caches both the value and the error so a missing asset is
// resolved once, not re-queried for every node that references it.
type lookupEntry[T any] struct {
	value T
	err   error
}

// LookupCache memoizes asset reads for exactly one publish/validation
// request. Callers must create a fresh cache per request: a cached version
// is deliberately frozen for the duration of that request, which is more
// deterministic than observing a mixture of versions between validation and
// resolution.
type LookupCache struct {
	mu                sync.Mutex
	profiles          map[string]lookupEntry[*ConfigProfile]
	schemas           map[string]lookupEntry[*DataSchema]
	mappings          map[string]lookupEntry[*Mapping]
	scripts           map[string]lookupEntry[*Script]
	scriptVersions    map[string]lookupEntry[[]ScriptVersion]
	scriptVersion     map[string]lookupEntry[*Script]
	connectors        map[string]lookupEntry[*Connector]
	connectorVersions map[string]lookupEntry[*Connector]
}

func NewLookupCache() *LookupCache {
	return &LookupCache{
		profiles:          map[string]lookupEntry[*ConfigProfile]{},
		schemas:           map[string]lookupEntry[*DataSchema]{},
		mappings:          map[string]lookupEntry[*Mapping]{},
		scripts:           map[string]lookupEntry[*Script]{},
		scriptVersions:    map[string]lookupEntry[[]ScriptVersion]{},
		scriptVersion:     map[string]lookupEntry[*Script]{},
		connectors:        map[string]lookupEntry[*Connector]{},
		connectorVersions: map[string]lookupEntry[*Connector]{},
	}
}

func versionKey(id string, version int) string {
	return id + "#" + strconv.Itoa(version)
}

func (c *LookupCache) ConfigProfile(s *Store, id string) (*ConfigProfile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.profiles[id]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetConfigProfile(id)
	c.profiles[id] = lookupEntry[*ConfigProfile]{value: value, err: err}
	return value, err
}

func (c *LookupCache) DataSchema(s *Store, id string) (*DataSchema, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.schemas[id]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetDataSchema(id)
	c.schemas[id] = lookupEntry[*DataSchema]{value: value, err: err}
	return value, err
}

func (c *LookupCache) Mapping(s *Store, id string) (*Mapping, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.mappings[id]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetMapping(id)
	c.mappings[id] = lookupEntry[*Mapping]{value: value, err: err}
	return value, err
}

func (c *LookupCache) Script(s *Store, id string) (*Script, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.scripts[id]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetScript(id)
	c.scripts[id] = lookupEntry[*Script]{value: value, err: err}
	return value, err
}

func (c *LookupCache) ScriptVersions(s *Store, id string) ([]ScriptVersion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.scriptVersions[id]; ok {
		return entry.value, entry.err
	}
	value, err := s.ListScriptVersions(id)
	c.scriptVersions[id] = lookupEntry[[]ScriptVersion]{value: value, err: err}
	return value, err
}

func (c *LookupCache) ScriptVersion(s *Store, id string, version int) (*Script, error) {
	key := versionKey(id, version)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.scriptVersion[key]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetScriptVersion(id, version)
	c.scriptVersion[key] = lookupEntry[*Script]{value: value, err: err}
	return value, err
}

func (c *LookupCache) Connector(s *Store, id string) (*Connector, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.connectors[id]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetConnector(id)
	c.connectors[id] = lookupEntry[*Connector]{value: value, err: err}
	return value, err
}

func (c *LookupCache) ConnectorVersion(s *Store, id string, version int) (*Connector, error) {
	key := versionKey(id, version)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.connectorVersions[key]; ok {
		return entry.value, entry.err
	}
	value, err := s.GetConnectorVersion(id, version)
	c.connectorVersions[key] = lookupEntry[*Connector]{value: value, err: err}
	return value, err
}

// SourceMatchesScriptVersion is the cached form of
// Store.sourceMatchesScriptVersion. Store errors still read as no match so
// the conservative "leave authored source alone" behavior is unchanged.
func (c *LookupCache) SourceMatchesScriptVersion(s *Store, id, source string) bool {
	versions, err := c.ScriptVersions(s, id)
	if err != nil {
		return false
	}
	for _, v := range versions {
		if sv, err := c.ScriptVersion(s, id, v.Version); err == nil && sv.Source == source {
			return true
		}
	}
	return false
}
