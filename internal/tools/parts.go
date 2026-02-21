package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Part represents an InvenTree part.
type Part struct {
	PK           int      `json:"pk"`
	Name         string   `json:"name"`
	FullName     string   `json:"full_name"`
	Description  string   `json:"description"`
	Category     *int     `json:"category"`
	CategoryName string   `json:"category_name"`
	Active       bool     `json:"active"`
	InStock      float64  `json:"in_stock"`
	TotalInStock float64  `json:"total_in_stock"`
	MinimumStock float64  `json:"minimum_stock"`
	Units        string   `json:"units"`
	IPN          string   `json:"IPN"`
	Keywords     *string  `json:"keywords"`
	Link         *string  `json:"link"`
	Purchaseable bool     `json:"purchaseable"`
	Assembly     bool     `json:"assembly"`
	Component    bool     `json:"component"`
	Trackable    bool     `json:"trackable"`
	Virtual      bool     `json:"virtual"`
	Image        *string  `json:"image"`
	Thumbnail    *string  `json:"thumbnail"`
	Tags         []string `json:"tags"`
}

// -- Search Parts --

type SearchPartsInput struct {
	Search string `json:"search" jsonschema:"Search query to find parts by name or keyword"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default 25)"`
}

func RegisterSearchParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "search_parts",
		Description: "Search for parts by name, keyword, or description. Use this to find existing parts before creating new ones. Returns matching parts with their IDs, names, categories, and stock levels.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/part/?search=%s&limit=%d&format=json", url.QueryEscape(input.Search), limit)
		var resp client.PaginatedResponse[Part]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Part --

type GetPartInput struct {
	ID int `json:"id" jsonschema:"The part ID (pk) to retrieve"`
}

func RegisterGetPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_part",
		Description: "Get detailed information about a specific part by its ID.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetPartInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/part/%d/?format=json", input.ID)
		var part Part
		if err := c.Get(path, &part); err != nil {
			return errResult(fmt.Errorf("getting part %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(part)
	})
}

// -- Create Part --

type CreatePartInput struct {
	Name         string `json:"name" jsonschema:"Part name (required)"`
	Description  string `json:"description,omitempty" jsonschema:"Part description"`
	Category     int    `json:"category,omitempty" jsonschema:"Category ID for the part. 0 or omit for uncategorized."`
	IPN          string `json:"IPN,omitempty" jsonschema:"Internal Part Number"`
	Keywords     string `json:"keywords,omitempty" jsonschema:"Keywords for search"`
	Units        string `json:"units,omitempty" jsonschema:"Units of measure"`
	MinimumStock int    `json:"minimum_stock,omitempty" jsonschema:"Minimum stock level"`
	Purchaseable *bool  `json:"purchaseable,omitempty" jsonschema:"Whether the part can be purchased (default true)"`
	Component    *bool  `json:"component,omitempty" jsonschema:"Whether the part is a component (default true)"`
	Assembly     *bool  `json:"assembly,omitempty" jsonschema:"Whether the part is an assembly"`
	Trackable    *bool  `json:"trackable,omitempty" jsonschema:"Whether the part is trackable by serial number"`
	Virtual      *bool  `json:"virtual,omitempty" jsonschema:"Whether the part is virtual (not physical)"`
	ImageURL     string `json:"image_url,omitempty" jsonschema:"URL of an image to attach to the part. InvenTree downloads it server-side."`
}

func RegisterCreatePart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "create_part",
		Description: "Create a new part in InvenTree. Returns the created part with its ID. IMPORTANT workflow: (1) search_parts to check for duplicates, (2) list_part_categories to see the FULL category hierarchy (check pathstring fields to understand nesting), (3) find the deepest, most specific category that fits this part — categories can be nested many levels deep (e.g. Electronic Components/Resistors/Through Hole/1/8 Watt), so always prefer the most specific match, (4) if no suitable category exists, create one with create_part_category under the most appropriate parent — do NOT put a part in an unrelated category just because it exists, and do NOT create top-level categories when the part belongs under an existing parent, (5) create the part with the correct category ID. Parts should always have a category.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreatePartInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"name": input.Name,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Category != 0 {
			payload["category"] = input.Category
		}
		if input.IPN != "" {
			payload["IPN"] = input.IPN
		}
		if input.Keywords != "" {
			payload["keywords"] = input.Keywords
		}
		if input.Units != "" {
			payload["units"] = input.Units
		}
		if input.MinimumStock > 0 {
			payload["minimum_stock"] = input.MinimumStock
		}
		if input.Purchaseable != nil {
			payload["purchaseable"] = *input.Purchaseable
		}
		if input.Component != nil {
			payload["component"] = *input.Component
		}
		if input.Assembly != nil {
			payload["assembly"] = *input.Assembly
		}
		if input.Trackable != nil {
			payload["trackable"] = *input.Trackable
		}
		if input.Virtual != nil {
			payload["virtual"] = *input.Virtual
		}
		if input.ImageURL != "" {
			payload["remote_image"] = input.ImageURL
		}

		var created Part
		if err := c.Post("/api/part/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Update Part --

type UpdatePartInput struct {
	ID           int    `json:"id" jsonschema:"The part ID (pk) to update"`
	Name         string `json:"name,omitempty" jsonschema:"New part name"`
	Description  string `json:"description,omitempty" jsonschema:"New description"`
	Category     int    `json:"category,omitempty" jsonschema:"New category ID. 0 or omit to leave unchanged."`
	Active       *bool  `json:"active,omitempty" jsonschema:"Whether the part is active"`
	IPN          string `json:"IPN,omitempty" jsonschema:"New Internal Part Number"`
	Keywords     string `json:"keywords,omitempty" jsonschema:"New keywords"`
	Units        string `json:"units,omitempty" jsonschema:"New units of measure"`
	MinimumStock int    `json:"minimum_stock,omitempty" jsonschema:"New minimum stock level. 0 or omit to leave unchanged."`
	ImageURL     string `json:"image_url,omitempty" jsonschema:"URL of an image to set for this part. InvenTree downloads it server-side."`
}

func RegisterUpdatePart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "update_part",
		Description: "Update an existing part's fields. Only provided fields are changed. Use this to rename parts, change categories, update descriptions, or deactivate parts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdatePartInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{}
		if input.Name != "" {
			payload["name"] = input.Name
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Category != 0 {
			payload["category"] = input.Category
		}
		if input.Active != nil {
			payload["active"] = *input.Active
		}
		if input.IPN != "" {
			payload["IPN"] = input.IPN
		}
		if input.Keywords != "" {
			payload["keywords"] = input.Keywords
		}
		if input.Units != "" {
			payload["units"] = input.Units
		}
		if input.MinimumStock != 0 {
			payload["minimum_stock"] = input.MinimumStock
		}
		if input.ImageURL != "" {
			payload["remote_image"] = input.ImageURL
		}

		if len(payload) == 0 {
			return errResult(fmt.Errorf("no fields to update")), nil, nil
		}

		var updated Part
		path := fmt.Sprintf("/api/part/%d/", input.ID)
		if err := c.Patch(path, payload, &updated); err != nil {
			return errResult(fmt.Errorf("updating part %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Delete Part --

type DeletePartInput struct {
	ID int `json:"id" jsonschema:"The part ID (pk) to delete"`
}

func RegisterDeletePart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "delete_part",
		Description: "Delete a part from InvenTree. This automatically deactivates the part first (required by InvenTree). The part must have no stock items. This is destructive and cannot be undone.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeletePartInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/part/%d/", input.ID)
		// InvenTree requires parts to be inactive before deletion
		if err := c.Patch(path, map[string]any{"active": false}, nil); err != nil {
			return errResult(fmt.Errorf("deactivating part %d before delete: %w", input.ID, err)), nil, nil
		}
		if err := c.Delete(path); err != nil {
			return errResult(fmt.Errorf("deleting part %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Part %d deleted successfully.", input.ID))
	})
}

// -- List Parts --

type ListPartsInput struct {
	Category int `json:"category,omitempty" jsonschema:"Filter by category ID. 0 or omit to list all."`
	Limit    int `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
	Offset   int `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterListParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_parts",
		Description: "List all parts, optionally filtered by category. Use search_parts for finding specific parts by name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/part/?limit=%d&offset=%d&format=json", limit, input.Offset)
		if input.Category != 0 {
			path += fmt.Sprintf("&category=%d", input.Category)
		}

		var resp client.PaginatedResponse[Part]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Set Part Image --

type SetPartImageInput struct {
	ID       int    `json:"id" jsonschema:"The part ID (pk) to set the image for"`
	ImageURL string `json:"image_url" jsonschema:"URL of the image. InvenTree downloads it server-side."`
}

func RegisterSetPartImage(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "set_part_image",
		Description: "Set or replace the image for an existing part by providing an image URL. InvenTree downloads the image from the URL server-side. Use this after search_part_images to attach a product photo to a part.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetPartImageInput) (*mcp.CallToolResult, any, error) {
		if input.ImageURL == "" {
			return errResult(fmt.Errorf("image_url is required")), nil, nil
		}
		payload := map[string]any{
			"remote_image": input.ImageURL,
		}
		var updated Part
		path := fmt.Sprintf("/api/part/%d/", input.ID)
		if err := c.Patch(path, payload, &updated); err != nil {
			return errResult(fmt.Errorf("setting image for part %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Search Part Images --

type SearchPartImagesInput struct {
	Query string `json:"query" jsonschema:"Search query for finding product images (e.g. part name, manufacturer part number)"`
	Num   int    `json:"num,omitempty" jsonschema:"Number of results to return (1-10, default 5)"`
}

func RegisterSearchPartImages(server *mcp.Server, imgClient *imagesearch.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "search_part_images",
		Description: "Search for product images using Google Custom Search. Returns image URLs that can be used with create_part (image_url field) or set_part_image. " +
			"Decision logic: If the top result clearly matches the part (e.g. exact product photo from manufacturer or distributor), use it directly. " +
			"If unsure, present all results to the user and let them choose. " +
			"Tip: include the manufacturer name or 'datasheet' in the query for better results. " +
			"Requires GOOGLE_API_KEY and GOOGLE_CSE_ID environment variables to be configured.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchPartImagesInput) (*mcp.CallToolResult, any, error) {
		if imgClient == nil {
			return errResult(fmt.Errorf("image search is not configured: set GOOGLE_API_KEY and GOOGLE_CSE_ID environment variables")), nil, nil
		}
		if input.Query == "" {
			return errResult(fmt.Errorf("query is required")), nil, nil
		}
		num := input.Num
		if num <= 0 {
			num = 5
		}
		results, err := imgClient.Search(input.Query, num)
		if err != nil {
			return errResult(fmt.Errorf("image search failed: %w", err)), nil, nil
		}
		if len(results) == 0 {
			return textResult("No images found for query: " + input.Query)
		}
		return jsonResult(map[string]any{
			"query":   input.Query,
			"count":   len(results),
			"results": results,
		})
	})
}

// -- helpers shared by all tool files --

func errResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	r.SetError(err)
	return r
}

func textResult(text string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(fmt.Errorf("marshaling result: %w", err)), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil, nil
}

func boolPtr(b bool) *bool { return &b }
