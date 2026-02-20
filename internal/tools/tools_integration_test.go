package tools_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setupServer(t *testing.T) (*mcp.Server, *client.Client) {
	t.Helper()
	url := os.Getenv("INVENTREE_URL")
	token := os.Getenv("INVENTREE_TOKEN")
	if url == "" || token == "" {
		t.Skip("INVENTREE_URL and INVENTREE_TOKEN must be set for integration tests")
	}

	c := client.New(url, token)
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	tools.RegisterAll(server, c)
	return server, c
}

// TestSearchParts tests searching for parts.
func TestSearchParts(t *testing.T) {
	_, c := setupServer(t)

	var resp client.PaginatedResponse[tools.Part]
	err := c.Get("/api/part/?search=pot&limit=10&format=json", &resp)
	if err != nil {
		t.Fatalf("search parts: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least one result for 'pot'")
	}
	t.Logf("Found %d parts matching 'pot'", resp.Count)
	for _, p := range resp.Results {
		t.Logf("  - [%d] %s (category: %s, in_stock: %.0f)", p.PK, p.Name, p.CategoryName, p.InStock)
	}
}

// TestSearchLocations tests location search with partial matching.
func TestSearchLocations(t *testing.T) {
	_, c := setupServer(t)

	var resp client.PaginatedResponse[tools.StockLocation]
	err := c.Get("/api/stock/location/?search=green&limit=10&format=json", &resp)
	if err != nil {
		t.Fatalf("search locations: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least one result for 'green'")
	}
	t.Logf("Found %d locations matching 'green'", resp.Count)
	for _, l := range resp.Results {
		t.Logf("  - [%d] %s (path: %s)", l.PK, l.Name, l.PathString)
	}
}

// TestSearchLocationsFuzzy tests that fuzzy search works for approximate names.
func TestSearchLocationsFuzzy(t *testing.T) {
	_, c := setupServer(t)

	// Test "blue" - should find "Blue 1"
	var resp client.PaginatedResponse[tools.StockLocation]
	err := c.Get("/api/stock/location/?search=blue&limit=10&format=json", &resp)
	if err != nil {
		t.Fatalf("search locations: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least one result for 'blue'")
	}
	t.Logf("Found %d locations matching 'blue': %s", resp.Count, resp.Results[0].Name)

	// Test "office" - should find "Office"
	err = c.Get("/api/stock/location/?search=office&limit=10&format=json", &resp)
	if err != nil {
		t.Fatalf("search locations: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least one result for 'office'")
	}
	t.Logf("Found %d locations matching 'office': %s", resp.Count, resp.Results[0].Name)
}

// TestListCategories tests listing part categories.
func TestListCategories(t *testing.T) {
	_, c := setupServer(t)

	var resp client.PaginatedResponse[tools.PartCategory]
	err := c.Get("/api/part/category/?limit=50&format=json", &resp)
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if resp.Count == 0 {
		t.Fatal("expected at least one category")
	}
	t.Logf("Found %d categories", resp.Count)
	for _, cat := range resp.Results {
		t.Logf("  - [%d] %s (path: %s, parts: %d)", cat.PK, cat.Name, cat.PathString, cat.PartCount)
	}
}

// TestCreateAndDeletePart tests the full lifecycle: create part → add stock → delete stock → delete part.
func TestCreateAndDeletePart(t *testing.T) {
	_, c := setupServer(t)

	// 1. Create a test part
	var created tools.Part
	err := c.Post("/api/part/", map[string]any{
		"name":        "_MCP_TEST_PART_DELETE_ME",
		"description": "Integration test part - safe to delete",
		"component":   true,
	}, &created)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	t.Logf("Created part: [%d] %s", created.PK, created.Name)

	// Cleanup: always delete the part at the end
	defer func() {
		// Delete any stock items for this part first
		var stockResp client.PaginatedResponse[tools.StockItem]
		if err := c.Get(fmt.Sprintf("/api/stock/?part=%d&limit=50&format=json", created.PK), &stockResp); err == nil {
			for _, s := range stockResp.Results {
				t.Logf("Cleaning up stock item [%d]", s.PK)
				_ = c.Delete(fmt.Sprintf("/api/stock/%d/", s.PK))
			}
		}
		// Must deactivate before deleting
		t.Logf("Deactivating part [%d]", created.PK)
		_ = c.Patch(fmt.Sprintf("/api/part/%d/", created.PK), map[string]any{"active": false}, nil)
		t.Logf("Deleting part [%d]", created.PK)
		if err := c.Delete(fmt.Sprintf("/api/part/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete part: %v", err)
		}
	}()

	// 2. Verify we can find it by search
	var searchResp client.PaginatedResponse[tools.Part]
	err = c.Get("/api/part/?search=_MCP_TEST_PART&limit=25&format=json", &searchResp)
	if err != nil {
		t.Fatalf("search for test part: %v", err)
	}
	if searchResp.Count == 0 {
		t.Fatal("created part not found via search")
	}
	t.Logf("Found test part via search: %s", searchResp.Results[0].Name)

	// 3. Add stock for the part in Green 1 (pk=8)
	var stockItems []tools.StockItem
	location := 8
	err = c.Post("/api/stock/", map[string]any{
		"part":     created.PK,
		"quantity": 5,
		"location": location,
	}, &stockItems)
	if err != nil {
		t.Fatalf("add stock: %v", err)
	}
	if len(stockItems) == 0 {
		t.Fatal("no stock items returned from creation")
	}
	stockItem := stockItems[0]
	t.Logf("Added stock item [%d]: qty=%.0f at location %d", stockItem.PK, stockItem.Quantity, location)

	// 4. Verify stock by querying
	var stockResp client.PaginatedResponse[tools.StockItem]
	err = c.Get(fmt.Sprintf("/api/stock/?part=%d&limit=50&format=json", created.PK), &stockResp)
	if err != nil {
		t.Fatalf("get stock: %v", err)
	}
	if stockResp.Count == 0 {
		t.Fatal("no stock found for test part")
	}
	t.Logf("Found %d stock items for test part", stockResp.Count)

	// 5. Delete the stock item
	err = c.Delete(fmt.Sprintf("/api/stock/%d/", stockItem.PK))
	if err != nil {
		t.Fatalf("delete stock: %v", err)
	}
	t.Logf("Deleted stock item [%d]", stockItem.PK)

	// Part deletion happens in defer
}

// TestCreateAndDeleteLocation tests location lifecycle.
func TestCreateAndDeleteLocation(t *testing.T) {
	_, c := setupServer(t)

	// Create a test location under Office (pk=2)
	parent := 2
	var created tools.StockLocation
	err := c.Post("/api/stock/location/", map[string]any{
		"name":        "_MCP_TEST_LOC_DELETE_ME",
		"description": "Integration test location - safe to delete",
		"parent":      parent,
	}, &created)
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	t.Logf("Created location: [%d] %s (path: %s)", created.PK, created.Name, created.PathString)

	defer func() {
		t.Logf("Deleting location [%d]", created.PK)
		if err := c.Delete(fmt.Sprintf("/api/stock/location/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete location: %v", err)
		}
	}()

	// Search should find it
	var searchResp client.PaginatedResponse[tools.StockLocation]
	err = c.Get("/api/stock/location/?search=_MCP_TEST_LOC&limit=25&format=json", &searchResp)
	if err != nil {
		t.Fatalf("search for test location: %v", err)
	}
	if searchResp.Count == 0 {
		t.Fatal("created location not found via search")
	}
	t.Logf("Found test location via search: %s", searchResp.Results[0].PathString)
}

// TestCreateAndDeleteCategory tests category lifecycle.
func TestCreateAndDeleteCategory(t *testing.T) {
	_, c := setupServer(t)

	// Create under Electronic Components (pk=1)
	parent := 1
	var created tools.PartCategory
	err := c.Post("/api/part/category/", map[string]any{
		"name":        "_MCP_TEST_CAT_DELETE_ME",
		"description": "Integration test category - safe to delete",
		"parent":      parent,
	}, &created)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	t.Logf("Created category: [%d] %s (path: %s)", created.PK, created.Name, created.PathString)

	defer func() {
		t.Logf("Deleting category [%d]", created.PK)
		if err := c.Delete(fmt.Sprintf("/api/part/category/%d/", created.PK)); err != nil {
			t.Errorf("cleanup - delete category: %v", err)
		}
	}()

	// Search should find it
	var searchResp client.PaginatedResponse[tools.PartCategory]
	err = c.Get("/api/part/category/?search=_MCP_TEST_CAT&limit=25&format=json", &searchResp)
	if err != nil {
		t.Fatalf("search for test category: %v", err)
	}
	if searchResp.Count == 0 {
		t.Fatal("created category not found via search")
	}
	t.Logf("Found test category via search: %s", searchResp.Results[0].PathString)
}

