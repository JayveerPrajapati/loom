package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ListConfigProfilesPage returns one keyset page of profiles, optionally
// narrowed by node type. The unpaged ListConfigProfiles is kept for boot
// indexes and pickers that need every asset.
func (s *Store) ListConfigProfilesPage(nodeType, query, cursor string, limit int, scope VisibilityScope) (Page[ConfigProfile], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if nodeType != "" {
		where = append(where, "p.node_type = ?")
		args = append(args, nodeType)
	}
	if clause, vargs := scope.clause("p"); clause != "1=1" {
		where = append(where, clause)
		args = append(args, vargs...)
	}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q),
			"p.name", "p.description",
			"json_extract(v.doc, '$.owner')", "json_extract(v.doc, '$.tags')")
	}
	from := ` FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id AND v.version = p.current_version`
	var page Page[ConfigProfile]
	if err := s.readRow(`SELECT COUNT(*)`+from+` WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[ConfigProfile]{}, err
	}
	if clause, cargs, err := ascNameCursorClause(cursor, "lower(p.name)", "p.id"); err != nil {
		return Page[ConfigProfile]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT p.id, p.name, p.node_type, p.description, p.current_version,
		p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM config_profile_usage u WHERE u.profile_id = p.id)`+from+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY lower(p.name), p.id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[ConfigProfile]{}, err
	}
	defer rows.Close()
	page.Items = []ConfigProfile{}
	for rows.Next() {
		var item ConfigProfile
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.NodeType, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return Page[ConfigProfile]{}, err
		}
		if err := scanConfigProfileFinish(&item, created, updated, doc); err != nil {
			return Page[ConfigProfile]{}, err
		}
		item.UsageCount = count
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[ConfigProfile]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(last.Name, last.ID)
	}
	return page, nil
}

// ListMappingsPage returns one keyset page of mappings, optionally narrowed
// to a set of mapping kinds (the hub's kind/nodeType filters).
func (s *Store) ListMappingsPage(kinds []string, query, cursor string, limit int, scope VisibilityScope) (Page[Mapping], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if len(kinds) > 0 {
		placeholders := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			placeholders = append(placeholders, "?")
			args = append(args, kind)
		}
		where = append(where, "p.kind IN ("+strings.Join(placeholders, ",")+")")
	}
	if clause, vargs := scope.clause("p"); clause != "1=1" {
		where = append(where, clause)
		args = append(args, vargs...)
	}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q),
			"p.name", "p.description",
			"json_extract(v.doc, '$.owner')", "json_extract(v.doc, '$.tags')")
	}
	from := ` FROM mappings p
		JOIN mapping_versions v ON v.mapping_id = p.id AND v.version = p.current_version`
	var page Page[Mapping]
	if err := s.readRow(`SELECT COUNT(*)`+from+` WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[Mapping]{}, err
	}
	if clause, cargs, err := ascNameCursorClause(cursor, "lower(p.name)", "p.id"); err != nil {
		return Page[Mapping]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.current_version,
		p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM mapping_usage u WHERE u.mapping_id = p.id)`+from+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY lower(p.name), p.id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[Mapping]{}, err
	}
	defer rows.Close()
	page.Items = []Mapping{}
	for rows.Next() {
		var item Mapping
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return Page[Mapping]{}, err
		}
		if err := scanMappingFinish(&item, created, updated, doc); err != nil {
			return Page[Mapping]{}, err
		}
		item.UsageCount = count
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Mapping]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(last.Name, last.ID)
	}
	return page, nil
}

// ListDataSchemasPage returns one keyset page of contracts.
func (s *Store) ListDataSchemasPage(query, cursor string, limit int, scope VisibilityScope) (Page[DataSchema], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if clause, vargs := scope.clause("p"); clause != "1=1" {
		where = append(where, clause)
		args = append(args, vargs...)
	}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q),
			"p.name", "p.description",
			"json_extract(v.doc, '$.owner')", "json_extract(v.doc, '$.tags')")
	}
	from := ` FROM data_schemas p
		JOIN data_schema_versions v ON v.schema_id = p.id AND v.version = p.current_version`
	var page Page[DataSchema]
	if err := s.readRow(`SELECT COUNT(*)`+from+` WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[DataSchema]{}, err
	}
	if clause, cargs, err := ascNameCursorClause(cursor, "lower(p.name)", "p.id"); err != nil {
		return Page[DataSchema]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.current_version,
		p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM data_schema_usage u WHERE u.schema_id = p.id)`+from+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY lower(p.name), p.id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[DataSchema]{}, err
	}
	defer rows.Close()
	page.Items = []DataSchema{}
	for rows.Next() {
		var item DataSchema
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return Page[DataSchema]{}, err
		}
		if err := scanDataSchemaFinish(&item, created, updated, doc); err != nil {
			return Page[DataSchema]{}, err
		}
		item.UsageCount = count
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[DataSchema]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(last.Name, last.ID)
	}
	return page, nil
}

// ListScriptsPage returns one keyset page of scripts, optionally narrowed
// by language.
func (s *Store) ListScriptsPage(language, query, cursor string, limit int, scope VisibilityScope) (Page[Script], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if language != "" {
		if !ValidScriptLanguage(language) {
			return Page[Script]{}, fmt.Errorf("unknown script language %q", language)
		}
		where = append(where, "p.language = ?")
		args = append(args, language)
	}
	if clause, vargs := scope.clause("p"); clause != "1=1" {
		where = append(where, clause)
		args = append(args, vargs...)
	}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q),
			"p.name", "p.description",
			"json_extract(v.doc, '$.owner')", "json_extract(v.doc, '$.tags')")
	}
	from := ` FROM scripts p
		JOIN script_versions v ON v.script_id = p.id AND v.version = p.current_version`
	var page Page[Script]
	if err := s.readRow(`SELECT COUNT(*)`+from+` WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[Script]{}, err
	}
	if clause, cargs, err := ascNameCursorClause(cursor, "lower(p.name)", "p.id"); err != nil {
		return Page[Script]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.language, p.current_version,
		p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM script_usage u WHERE u.script_id = p.id)`+from+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY lower(p.name), p.id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[Script]{}, err
	}
	defer rows.Close()
	page.Items = []Script{}
	for rows.Next() {
		var item Script
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Language, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return Page[Script]{}, err
		}
		if err := scanScriptFinish(&item, created, updated, doc); err != nil {
			return Page[Script]{}, err
		}
		item.UsageCount = count
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Script]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(last.Name, last.ID)
	}
	return page, nil
}

// ListConnectorsPage returns one keyset page of connectors. Operations and
// usage are hydrated for the page in two batched queries instead of two
// queries per row.
func (s *Store) ListConnectorsPage(query, cursor string, limit int, scope VisibilityScope) (Page[Connector], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if clause, vargs := scope.clause("connectors"); clause != "1=1" {
		where = append(where, clause)
		args = append(args, vargs...)
	}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q), "name", "description", "owner", "tags")
	}
	var page Page[Connector]
	if err := s.readRow(`SELECT COUNT(*) FROM connectors WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[Connector]{}, err
	}
	if clause, cargs, err := ascNameCursorClause(cursor, "lower(name)", "id"); err != nil {
		return Page[Connector]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT `+connectorColumns+` FROM connectors
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY lower(name), id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[Connector]{}, err
	}
	defer rows.Close()
	page.Items = []Connector{}
	for rows.Next() {
		d, err := scanConnectorRow(rows)
		if err != nil {
			return Page[Connector]{}, err
		}
		page.Items = append(page.Items, *d)
	}
	if err := rows.Err(); err != nil {
		return Page[Connector]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(last.Name, last.ID)
	}
	if err := s.hydrateConnectorPage(page.Items); err != nil {
		return Page[Connector]{}, err
	}
	return page, nil
}

func connectorPageKey(id string, version int) string {
	return fmt.Sprintf("%s\x00%d", id, version)
}

func (s *Store) hydrateConnectorPage(items []Connector) error {
	if len(items) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(items))
	args := make([]any, 0, len(items)*2)
	for _, c := range items {
		placeholders = append(placeholders, "(?, ?)")
		args = append(args, c.ID, c.Version)
	}
	rows, err := s.read(`SELECT connector_id, version, doc FROM connector_versions
		WHERE (connector_id, version) IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return err
	}
	operations := map[string][]ConnectorOperation{}
	for rows.Next() {
		var id string
		var version int
		var doc []byte
		if err := rows.Scan(&id, &version, &doc); err != nil {
			rows.Close()
			return err
		}
		var parsed connectorDoc
		if json.Unmarshal(doc, &parsed) == nil {
			operations[connectorPageKey(id, version)] = parsed.Operations
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	idArgs := make([]any, 0, len(items))
	idPlaceholders := make([]string, 0, len(items))
	for _, c := range items {
		idArgs = append(idArgs, c.ID)
		idPlaceholders = append(idPlaceholders, "?")
	}
	usage := map[string]int{}
	usageRows, err := s.read(`SELECT connector_id, COUNT(*) FROM connector_usage
		WHERE connector_id IN (`+strings.Join(idPlaceholders, ",")+`) GROUP BY connector_id`, idArgs...)
	if err != nil {
		return err
	}
	for usageRows.Next() {
		var id string
		var count int
		if err := usageRows.Scan(&id, &count); err != nil {
			usageRows.Close()
			return err
		}
		usage[id] = count
	}
	if err := usageRows.Err(); err != nil {
		usageRows.Close()
		return err
	}
	usageRows.Close()
	for i := range items {
		items[i].Operations = operations[connectorPageKey(items[i].ID, items[i].Version)]
		items[i].UsageCount = usage[items[i].ID]
	}
	return nil
}

// ListWorkflowsPage returns one keyset page of workflow summaries.
func (s *Store) ListWorkflowsPage(query, cursor string, limit int) (Page[Summary], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q), "w.name", "json_extract(w.doc, '$.description')")
	}
	var page Page[Summary]
	if err := s.readRow(`SELECT COUNT(*) FROM workflows w WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[Summary]{}, err
	}
	if clause, cargs, err := ascNameCursorClause(cursor, "lower(w.name)", "w.id"); err != nil {
		return Page[Summary]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT w.id, w.name, w.version, w.published_version, w.updated_at, w.doc,
		v.content_hash,
		(SELECT COUNT(*) FROM jobs j WHERE j.template_id = w.id),
		(SELECT COUNT(*) FROM jobs j WHERE j.template_id = w.id AND j.enabled = 1)
		FROM workflows w
		LEFT JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.published_version
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY lower(w.name), w.id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[Summary]{}, err
	}
	defer rows.Close()
	page.Items = []Summary{}
	for rows.Next() {
		var item Summary
		var updated string
		var doc []byte
		var publishedHash sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.Version, &item.PublishedVersion,
			&updated, &doc, &publishedHash, &item.JobCount, &item.ActiveJobCount); err != nil {
			return Page[Summary]{}, err
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		item.Draft = item.PublishedVersion == 0 || !publishedHash.Valid ||
			publishedHash.String != contentHash(doc)
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Summary]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(last.Name, last.ID)
	}
	return page, nil
}

// ListCredentialsPage returns one keyset page of credential metadata. The
// search never touches secret material; only the id and type are exposed.
func (s *Store) ListCredentialsPage(query, cursor string, limit int) (Page[CredentialInfo], error) {
	limit = normalizePageLimit(limit)
	where := []string{"1=1"}
	args := []any{}
	if q := strings.TrimSpace(query); q != "" {
		appendSearch(&where, &args, likePattern(q), "id", "type")
	}
	var page Page[CredentialInfo]
	if err := s.readRow(`SELECT COUNT(*) FROM credentials WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return Page[CredentialInfo]{}, err
	}
	if clause, cargs, err := descCursorClause(cursor, "created_at", "id"); err != nil {
		return Page[CredentialInfo]{}, err
	} else if clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	rows, err := s.read(`SELECT id, type, created_at FROM credentials
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return Page[CredentialInfo]{}, err
	}
	defer rows.Close()
	page.Items = []CredentialInfo{}
	createdKeys := make([]string, 0, limit+1)
	for rows.Next() {
		var item CredentialInfo
		var created string
		if err := rows.Scan(&item.ID, &item.Type, &created); err != nil {
			return Page[CredentialInfo]{}, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		createdKeys = append(createdKeys, created)
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[CredentialInfo]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeListCursor(createdKeys[len(page.Items)-1], last.ID)
	}
	return page, nil
}
