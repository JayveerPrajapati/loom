package store

import (
	"errors"
	"testing"

	"loom/internal/schemas"
)

func TestDataSchemaCRUDAndVersioning(t *testing.T) {
	st := testProfileStore(t)

	fields := []schemas.Field{
		{Name: "email", Type: schemas.TypeString, Required: true},
		{Name: "name", Type: schemas.TypeString},
	}
	created, err := st.CreateDataSchema(&DataSchema{
		ID: "schema-1", Name: "Webhook lead", Description: "Inbound lead payload",
		Fields: fields,
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}

	got, err := st.GetDataSchema("schema-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Webhook lead" || len(got.Fields) != 2 {
		t.Fatalf("get returned %#v", got)
	}

	updated, err := st.UpdateDataSchema(&DataSchema{
		ID: "schema-1", Name: "Webhook lead v2", Fields: append(fields,
			schemas.Field{Name: "company", Type: schemas.TypeString}),
	}, 1, "user-2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated version = %d, want 2", updated.Version)
	}

	// Append-only history: version 1 is still readable and unchanged.
	v1, err := st.GetDataSchemaVersion("schema-1", 1)
	if err != nil {
		t.Fatalf("get version 1: %v", err)
	}
	if len(v1.Fields) != 2 || v1.Name != "Webhook lead" {
		t.Fatalf("version 1 mutated: %#v", v1)
	}

	versions, err := st.ListDataSchemaVersions("schema-1")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions: %#v", versions)
	}
	if versions[0].Actor != "user-2" || versions[1].Actor != "user-1" {
		t.Fatalf("version actors: %#v", versions)
	}

	// Stale update is rejected with the sentinel, not a blind overwrite.
	_, err = st.UpdateDataSchema(&DataSchema{ID: "schema-1", Name: "stale", Fields: fields}, 1, "user-3")
	if !errors.Is(err, ErrStaleDataSchema) {
		t.Fatalf("stale update error = %v", err)
	}
}

func TestDataSchemaDeleteGuardAndUsage(t *testing.T) {
	st := testProfileStore(t)

	created, err := st.CreateDataSchema(&DataSchema{
		ID: "schema-2", Name: "Order", Fields: []schemas.Field{{Name: "id", Type: schemas.TypeString, Required: true}},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Usage pins guard deletes (delete guard via the usage index).
	if err := st.RecordDataSchemaUsage(created.ID, created.Version, "wf-1", 3); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	err = st.DeleteDataSchema(created.ID)
	if !errors.Is(err, ErrDataSchemaInUse) {
		t.Fatalf("delete in-use error = %v", err)
	}

	usage, err := st.DataSchemaUsage(created.ID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(usage) != 1 || usage[0].WorkflowID != "wf-1" || usage[0].SchemaVersion != 1 {
		t.Fatalf("usage rows: %#v", usage)
	}

	list, err := st.ListDataSchemas()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].UsageCount != 1 {
		t.Fatalf("usage count: %#v", list)
	}

	// A workflow that no longer exists still reports its id (COALESCE keeps it).
	// After removing the usage rows, delete succeeds.
	if _, err := st.db.Exec(`DELETE FROM data_schema_usage`); err != nil {
		t.Fatalf("clear usage: %v", err)
	}
	if err := st.DeleteDataSchema(created.ID); err != nil {
		t.Fatalf("delete after unpublish: %v", err)
	}
	if _, err := st.GetDataSchema(created.ID); !errors.Is(err, ErrDataSchemaNotFound) {
		t.Fatalf("get after delete error = %v", err)
	}
	if err := st.DeleteDataSchema("missing"); !errors.Is(err, ErrDataSchemaNotFound) {
		t.Fatalf("delete missing error = %v", err)
	}
}

func TestDataSchemaDocRountripNested(t *testing.T) {
	st := testProfileStore(t)
	nested := []schemas.Field{
		{Name: "customer", Type: schemas.TypeObject, Required: true, Fields: []schemas.Field{
			{Name: "email", Type: schemas.TypeString, Required: true},
			{Name: "vip", Type: schemas.TypeBoolean},
		}},
		{Name: "items", Type: schemas.TypeArray, Items: &schemas.Field{
			Name: "item", Type: schemas.TypeObject, Fields: []schemas.Field{
				{Name: "sku", Type: schemas.TypeString, Required: true},
				{Name: "qty", Type: schemas.TypeNumber},
			},
		}},
	}
	created, err := st.CreateDataSchema(&DataSchema{
		ID: "schema-3", Name: "Nested", Fields: nested, Tags: []string{"orders"}, Owner: "team-a",
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.GetDataSchema(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("fields: %#v", got.Fields)
	}
	if got.Fields[0].Name != "customer" || len(got.Fields[0].Fields) != 2 || !got.Fields[0].Required {
		t.Fatalf("nested object lost: %#v", got.Fields[0])
	}
	if got.Fields[1].Items == nil || got.Fields[1].Items.Fields[0].Name != "sku" {
		t.Fatalf("array items lost: %#v", got.Fields[1])
	}
	if len(got.Tags) != 1 || got.Tags[0] != "orders" || got.Owner != "team-a" {
		t.Fatalf("governance fields lost: %#v", got)
	}
}
