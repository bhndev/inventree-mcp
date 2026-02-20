package tools

import (
	"context"
	"fmt"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PartCategory represents an InvenTree part category.
type PartCategory struct {
	PK              int      `json:"pk"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Parent          *int     `json:"parent"`
	PathString      string   `json:"pathstring"`
	Level           int      `json:"level"`
	PartCount       int      `json:"part_count"`
	Subcategories   int      `json:"subcategories"`
	Starred         bool     `json:"starred"`
	Structural      bool     `json:"structural"`
	Icon            string   `json:"icon"`
	DefaultLocation *int     `json:"default_location"`
}

// -- Search Part Categories --

type SearchCategoriesInput struct {
	Search string `json:"search" jsonschema:"Search query to find categories by name"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 25)"`
}

func RegisterSearchCategories(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_part_categories",
		Description: "Search for part categories by name. Use this to find the right category when creating parts. Returns categories with their full path (e.g., 'Electronic Components/Resistors/Through Hole').",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCategoriesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/part/category/?search=%s&limit=%d&format=json", input.Search, limit)
		var resp client.PaginatedResponse[PartCategory]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching categories: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- List Part Categories --

type ListCategoriesInput struct {
	Parent *int `json:"parent,omitempty" jsonschema:"Filter by parent category ID"`
	Limit  int  `json:"limit,omitempty" jsonschema:"Maximum number of results (default 100)"`
	Offset int  `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterListCategories(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_part_categories",
		Description: "List all part categories, optionally filtered by parent category. Shows the category hierarchy with pathstrings and part counts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListCategoriesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		path := fmt.Sprintf("/api/part/category/?limit=%d&offset=%d&format=json", limit, input.Offset)
		if input.Parent != nil {
			path += fmt.Sprintf("&parent=%d", *input.Parent)
		}

		var resp client.PaginatedResponse[PartCategory]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing categories: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Create Part Category --

type CreateCategoryInput struct {
	Name            string `json:"name" jsonschema:"Category name (required)"`
	Description     string `json:"description,omitempty" jsonschema:"Category description"`
	Parent          *int   `json:"parent,omitempty" jsonschema:"Parent category ID. Omit for top-level category."`
	DefaultLocation *int   `json:"default_location,omitempty" jsonschema:"Default stock location ID for parts in this category"`
	Structural      *bool  `json:"structural,omitempty" jsonschema:"If true, parts cannot be directly assigned to this category (only to sub-categories)"`
}

func RegisterCreateCategory(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_part_category",
		Description: "Create a new part category. Categories organize parts into a hierarchy (e.g., Electronic Components > Resistors > Through Hole). Always search for existing categories first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateCategoryInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"name": input.Name,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Parent != nil {
			payload["parent"] = *input.Parent
		}
		if input.DefaultLocation != nil {
			payload["default_location"] = *input.DefaultLocation
		}
		if input.Structural != nil {
			payload["structural"] = *input.Structural
		}

		var created PartCategory
		if err := c.Post("/api/part/category/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating category: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Delete Part Category --

type DeleteCategoryInput struct {
	ID int `json:"id" jsonschema:"The category ID (pk) to delete"`
}

func RegisterDeleteCategory(server *mcp.Server, c *client.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_part_category",
		Description: "Delete a part category. The category must have no parts or sub-categories.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteCategoryInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/part/category/%d/", input.ID)
		if err := c.Delete(path); err != nil {
			return errResult(fmt.Errorf("deleting category %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Category %d deleted successfully.", input.ID))
	})
}
