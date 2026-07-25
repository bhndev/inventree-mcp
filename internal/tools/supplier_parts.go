package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ManufacturerPart links an internal Part to the company that makes it,
// carrying that manufacturer's own part number (MPN).
type ManufacturerPart struct {
	PK           int    `json:"pk"`
	Part         int    `json:"part"`
	Manufacturer int    `json:"manufacturer"`
	MPN          string `json:"MPN"`
	Description  string `json:"description"`
	Link         string `json:"link"`
}

// SupplierPart links an internal Part to a vendor's catalogue entry,
// carrying that vendor's SKU. A part may have many supplier parts.
type SupplierPart struct {
	PK               int     `json:"pk"`
	Part             int     `json:"part"`
	Supplier         int     `json:"supplier"`
	SKU              string  `json:"SKU"`
	ManufacturerPart *int    `json:"manufacturer_part"`
	Description      string  `json:"description"`
	Link             string  `json:"link"`
	Note             string  `json:"note"`
	Packaging        string  `json:"packaging"`
	PackQuantity     string  `json:"pack_quantity"`
	InStock          float64 `json:"in_stock"`
	Active           bool    `json:"active"`
}

// -- List Manufacturer Parts --

type ListManufacturerPartsInput struct {
	Part         int    `json:"part,omitempty" jsonschema:"Filter by internal part ID. 0 or omit for all."`
	Manufacturer int    `json:"manufacturer,omitempty" jsonschema:"Filter by manufacturer company ID. 0 or omit for all."`
	MPN          string `json:"mpn,omitempty" jsonschema:"Filter by exact manufacturer part number"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterListManufacturerParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_manufacturer_parts",
		Description: "List manufacturer part records, which map internal parts to a manufacturer's own part number (MPN). Filter by part to see who makes it, or by MPN to find the internal part for a manufacturer number.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListManufacturerPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/company/part/manufacturer/?limit=%d&format=json", limit)
		if input.Part != 0 {
			path += fmt.Sprintf("&part=%d", input.Part)
		}
		if input.Manufacturer != 0 {
			path += fmt.Sprintf("&manufacturer=%d", input.Manufacturer)
		}
		if input.MPN != "" {
			path += "&MPN=" + url.QueryEscape(input.MPN)
		}

		var resp client.PaginatedResponse[ManufacturerPart]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing manufacturer parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Create Manufacturer Part --

type CreateManufacturerPartInput struct {
	Part         int    `json:"part" jsonschema:"Internal part ID (required)"`
	Manufacturer int    `json:"manufacturer" jsonschema:"Manufacturer company ID (required). Must be a company with is_manufacturer set."`
	MPN          string `json:"mpn" jsonschema:"Manufacturer part number (required)"`
	Description  string `json:"description,omitempty" jsonschema:"Optional description"`
	Link         string `json:"link,omitempty" jsonschema:"Optional URL to the manufacturer's product page"`
}

func RegisterCreateManufacturerPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_manufacturer_part",
		Description: "Link an internal part to its manufacturer and that manufacturer's part number (MPN). " +
			"Do this before creating a supplier part so the vendor SKU can be tied back to the manufacturer number. " +
			"Check list_manufacturer_parts first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateManufacturerPartInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 || input.Manufacturer == 0 || input.MPN == "" {
			return errResult(fmt.Errorf("part, manufacturer and mpn are all required")), nil, nil
		}

		payload := map[string]any{
			"part":         input.Part,
			"manufacturer": input.Manufacturer,
			"MPN":          input.MPN,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}

		var created ManufacturerPart
		if err := c.Post("/api/company/part/manufacturer/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating manufacturer part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Search Supplier Parts --

type SearchSupplierPartsInput struct {
	Search   string `json:"search,omitempty" jsonschema:"Search text matched against SKU and description"`
	Part     int    `json:"part,omitempty" jsonschema:"Filter by internal part ID. 0 or omit for all."`
	Supplier int    `json:"supplier,omitempty" jsonschema:"Filter by supplier company ID. 0 or omit for all."`
	SKU      string `json:"sku,omitempty" jsonschema:"Filter by exact vendor SKU"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterSearchSupplierParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "search_supplier_parts",
		Description: "Find supplier part records, which map internal parts to a vendor's SKU. " +
			"Purchase order lines reference supplier parts, not parts directly, so use this to get the supplier part ID before adding an order line.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchSupplierPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/company/part/?limit=%d&format=json", limit)
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}
		if input.Part != 0 {
			path += fmt.Sprintf("&part=%d", input.Part)
		}
		if input.Supplier != 0 {
			path += fmt.Sprintf("&supplier=%d", input.Supplier)
		}
		if input.SKU != "" {
			path += "&SKU=" + url.QueryEscape(input.SKU)
		}

		var resp client.PaginatedResponse[SupplierPart]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching supplier parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Create Supplier Part --

type CreateSupplierPartInput struct {
	Part             int    `json:"part" jsonschema:"Internal part ID (required)"`
	Supplier         int    `json:"supplier" jsonschema:"Supplier company ID (required). Must be a company with is_supplier set."`
	SKU              string `json:"sku" jsonschema:"Vendor stock keeping unit, e.g. the Digikey part number (required)"`
	ManufacturerPart int    `json:"manufacturer_part,omitempty" jsonschema:"Manufacturer part ID to associate. 0 or omit for none."`
	Description      string `json:"description,omitempty" jsonschema:"Optional description"`
	Link             string `json:"link,omitempty" jsonschema:"Optional URL to the vendor's product page"`
	Note             string `json:"note,omitempty" jsonschema:"Free-text note, e.g. substitution history"`
	Packaging        string `json:"packaging,omitempty" jsonschema:"Packaging description, e.g. Tape and Reel"`
}

func RegisterCreateSupplierPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_supplier_part",
		Description: "Create a supplier part: the link between an internal part and a vendor's SKU. " +
			"Required before that part can appear on a purchase order from that vendor. " +
			"Search with search_supplier_parts first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateSupplierPartInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 || input.Supplier == 0 || input.SKU == "" {
			return errResult(fmt.Errorf("part, supplier and sku are all required")), nil, nil
		}

		payload := map[string]any{
			"part":     input.Part,
			"supplier": input.Supplier,
			"SKU":      input.SKU,
			"active":   true,
		}
		if input.ManufacturerPart != 0 {
			payload["manufacturer_part"] = input.ManufacturerPart
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.Note != "" {
			payload["note"] = input.Note
		}
		if input.Packaging != "" {
			payload["packaging"] = input.Packaging
		}

		var created SupplierPart
		if err := c.Post("/api/company/part/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating supplier part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}
