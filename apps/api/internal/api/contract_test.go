package api

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Sham607/network-orchestrator/apps/api/internal/inventory"
)

// The contract is the source of truth for this package, and `redocly lint` only
// checks the document against itself — it never looks at what the Go handlers
// return. These tests close that gap: the bytes a handler actually writes are
// validated against the schemas in openapi/openapi.yaml.

const contractPath = "../../../../openapi/openapi.yaml"

func contractYAML(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read %s: %v", contractPath, err)
	}
	return raw
}

func contractDoc(t *testing.T) any {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(contractYAML(t), &doc); err != nil {
		t.Fatalf("parse %s: %v", contractPath, err)
	}
	return doc
}

func contractObject(t *testing.T, node any, path ...string) map[string]any {
	t.Helper()
	current := node
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%v is not an object in %s", path, contractPath)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("%s is missing from %s", key, contractPath)
		}
	}
	object, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("%v is not an object in %s", path, contractPath)
	}
	return object
}

// contractSchema compiles one component schema, with format assertions on: the
// contract declares `format: uuid` and `format: date-time`, and those are part
// of what the handlers must satisfy.
func contractSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("openapi.yaml", contractDoc(t)); err != nil {
		t.Fatalf("register the contract: %v", err)
	}
	schema, err := compiler.Compile("openapi.yaml#/components/schemas/" + name)
	if err != nil {
		t.Fatalf("compile components.schemas.%s: %v", name, err)
	}
	return schema
}

// assertMatchesContract validates a response body against a component schema.
func assertMatchesContract(t *testing.T, schemaName, body string) {
	t.Helper()
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("decode the response body: %v", err)
	}
	if err := contractSchema(t, schemaName).Validate(instance); err != nil {
		t.Fatalf("response violates components.schemas.%s: %v\nbody: %s", schemaName, err, body)
	}
}

// contractProperties lists the properties a component schema declares.
func contractProperties(t *testing.T, name string) []string {
	t.Helper()
	properties := contractObject(t, contractDoc(t), "components", "schemas", name, "properties")
	names := make([]string, 0, len(properties))
	for property := range properties {
		names = append(names, property)
	}
	sort.Strings(names)
	return names
}

func objectKeys(t *testing.T, object map[string]any) []string {
	t.Helper()
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// --- response bodies against the contract ----------------------------------

func TestSiteListResponseMatchesTheContract(t *testing.T) {
	rr := get(t, listRouter(t, &fakeInventory{sites: sampleSites()}), "/api/v1/sites")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	assertMatchesContract(t, "SiteList", rr.Body.String())
}

func TestDeviceListResponseMatchesTheContract(t *testing.T) {
	rr := get(t, listRouter(t, &fakeInventory{devices: sampleDevices()}), "/api/v1/devices")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	assertMatchesContract(t, "DeviceList", rr.Body.String())
}

func TestEmptyResponsesMatchTheContract(t *testing.T) {
	inv := &fakeInventory{
		sites:   inventory.SitePage{Items: []inventory.Site{}},
		devices: inventory.DevicePage{Items: []inventory.Device{}},
	}
	handler := listRouter(t, inv)

	// The contract's `empty` example: an empty cache is a 200 with data [] and a
	// zero total, not a 404 and not a null.
	for _, endpoint := range []string{"sites", "devices"} {
		rr := get(t, handler, "/api/v1/"+endpoint)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", endpoint, rr.Code)
		}
		schemaName := "SiteList"
		if endpoint == "devices" {
			schemaName = "DeviceList"
		}
		assertMatchesContract(t, schemaName, rr.Body.String())
	}
}

func TestValidationErrorMatchesTheContract(t *testing.T) {
	rr := get(t, listRouter(t, &fakeInventory{}), "/api/v1/sites?page_size=201")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	assertMatchesContract(t, "Error", rr.Body.String())
}

func TestServiceUnavailableMatchesTheContract(t *testing.T) {
	inv := &fakeInventory{siteErr: errors.New("the database is gone")}
	rr := get(t, listRouter(t, inv), "/api/v1/sites")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	assertMatchesContract(t, "Error", rr.Body.String())
}

// TestListBodiesExposeOnlyContractFields is the data-leak guard: every key the
// transport writes is declared by the contract, so a raw UniFi payload, a UniFi
// id or a console id cannot reach the dashboard by being added to the cache.
func TestListBodiesExposeOnlyContractFields(t *testing.T) {
	handler := listRouter(t, &fakeInventory{sites: sampleSites(), devices: sampleDevices()})

	cases := map[string]struct {
		endpoint   string
		listSchema string
		rowSchema  string
	}{
		"sites":   {"/api/v1/sites", "SiteList", "Site"},
		"devices": {"/api/v1/devices", "DeviceList", "Device"},
	}

	for name, tc := range cases {
		body := decodeBody(t, get(t, handler, tc.endpoint))

		if got, want := objectKeys(t, body), contractProperties(t, tc.listSchema); !equalStrings(got, want) {
			t.Errorf("%s: top-level keys = %v, want the contract's %v", name, got, want)
		}

		data, _ := body["data"].([]any)
		if len(data) == 0 {
			t.Fatalf("%s: no rows to inspect", name)
		}
		want := contractProperties(t, tc.rowSchema)
		for _, entry := range data {
			row, _ := entry.(map[string]any)
			if got := objectKeys(t, row); !equalStrings(got, want) {
				t.Errorf("%s: row keys = %v, want the contract's %v", name, got, want)
			}
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// --- the contract's error table --------------------------------------------

// errorTableRow matches a row of the code→HTTP table in info.description. The
// table is indented inside the YAML block scalar, so the match anchors on the
// first pipe rather than on the line start.
var errorTableRow = regexp.MustCompile("(?m)^[^|\\n]*\\| `([a-z_]+)` \\| ([0-9]{3}) \\|")

// TestErrorCodeTableMatchesTheGoMapping keeps the contract's table and
// httpStatus in step. A code declared in the Error enum but missing from the
// table is a contract bug clients hit (that is exactly what unifi_rate_limited
// was), and a status that disagrees with the Go mapping is a live mismatch
// between what the contract promises and what the API answers.
func TestErrorCodeTableMatchesTheGoMapping(t *testing.T) {
	table := map[string]int{}
	for _, match := range errorTableRow.FindAllStringSubmatch(string(contractYAML(t)), -1) {
		status, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatalf("unparsable status %q for %q", match[2], match[1])
		}
		table[match[1]] = status
	}
	if len(table) == 0 {
		t.Fatal("the code→HTTP table was not found in the contract")
	}

	code := contractObject(t, contractDoc(t), "components", "schemas", "Error", "properties", "code")
	enum, _ := code["enum"].([]any)
	if len(enum) == 0 {
		t.Fatal("components.schemas.Error.properties.code has no enum")
	}

	for _, entry := range enum {
		name := entry.(string)
		status, ok := table[name]
		if !ok {
			t.Errorf("code %q is declared in the Error schema but has no row in the code→HTTP table", name)
			continue
		}
		if want := httpStatus(Code(name)); status != want {
			t.Errorf("contract maps %q to %d, httpStatus returns %d", name, status, want)
		}
	}
	if len(table) != len(enum) {
		t.Errorf("the table has %d rows but the Error enum declares %d codes", len(table), len(enum))
	}
}

// TestSortParametersMatchContractRule ensures the 4 inline sort query parameters
// across /sites, /devices, /clients, and /networks maintain an identical structure,
// differing only in their domain-specific enum and default values.
func TestSortParametersMatchContractRule(t *testing.T) {
	doc := contractDoc(t)
	paths := []string{"/sites", "/devices", "/clients", "/networks"}

	for _, p := range paths {
		pathObj := contractObject(t, doc, "paths", p, "get")
		params, ok := pathObj["parameters"].([]any)
		if !ok {
			t.Fatalf("%s get has no parameters slice", p)
		}

		var sortParam map[string]any
		for _, param := range params {
			pm, ok := param.(map[string]any)
			if !ok {
				continue
			}
			if pm["name"] == "sort" {
				sortParam = pm
				break
			}
		}

		if sortParam == nil {
			t.Fatalf("%s is missing the sort query parameter", p)
		}

		if got, want := sortParam["in"], "query"; got != want {
			t.Errorf("%s sort param: in = %v, want %v", p, got, want)
		}
		if got, want := sortParam["required"], false; got != want {
			t.Errorf("%s sort param: required = %v, want %v", p, got, want)
		}
		if got, want := sortParam["description"], "Field to sort by."; got != want {
			t.Errorf("%s sort param: description = %v, want %v", p, got, want)
		}

		schema, ok := sortParam["schema"].(map[string]any)
		if !ok {
			t.Fatalf("%s sort param has missing or invalid schema", p)
		}
		if got, want := schema["type"], "string"; got != want {
			t.Errorf("%s sort param schema: type = %v, want %v", p, got, want)
		}

		enum, ok := schema["enum"].([]any)
		if !ok || len(enum) == 0 {
			t.Errorf("%s sort param schema: enum must be non-empty slice", p)
		}

		def, ok := schema["default"].(string)
		if !ok || def == "" {
			t.Errorf("%s sort param schema: default must be non-empty string", p)
		}

		found := false
		for _, e := range enum {
			if e == def {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s sort param schema: default %q not found in enum %v", p, def, enum)
		}

		expectedParamKeys := []string{"description", "in", "name", "required", "schema"}
		if got := objectKeys(t, sortParam); !equalStrings(got, expectedParamKeys) {
			t.Errorf("%s sort param has unexpected keys: %v, want %v", p, got, expectedParamKeys)
		}

		expectedSchemaKeys := []string{"default", "enum", "type"}
		if got := objectKeys(t, schema); !equalStrings(got, expectedSchemaKeys) {
			t.Errorf("%s sort param schema has unexpected keys: %v, want %v", p, got, expectedSchemaKeys)
		}
	}
}
