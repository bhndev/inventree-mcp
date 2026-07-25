package tools

import (
	"context"
	"fmt"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// -- Get BOM --

type GetBOMInput struct {
	Part  int `json:"part" jsonschema:"Part ID of the ASSEMBLY whose BOM you want (required)"`
	Limit int `json:"limit,omitempty" jsonschema:"Maximum number of BOM lines (default 200)"`
}

func RegisterGetBOM(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_bom",
		Description: "Get the bill of materials for an assembly part -- the list of components and quantities needed to build one unit. " +
			"Call this before create_build_order to confirm the assembly actually has a BOM, and to see what components a build will consume. " +
			"Returns one entry per BOM line, each with its own pk which is what update_bom_item and delete_bom_item take.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetBOMInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 200
		}

		var resp client.PaginatedResponse[map[string]any]
		path := fmt.Sprintf("/api/bom/?part=%d&limit=%d&format=json", input.Part, limit)
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("getting BOM for part %d: %w", input.Part, err)), nil, nil
		}
		return jsonResult(map[string]any{
			"part":    input.Part,
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Add BOM Item --

type AddBOMItemInput struct {
	Part          int     `json:"part" jsonschema:"Part ID of the ASSEMBLY this line belongs to (required). Must have assembly=true."`
	SubPart       int     `json:"sub_part" jsonschema:"Part ID of the COMPONENT being consumed (required). Must have component=true."`
	Quantity      float64 `json:"quantity" jsonschema:"Quantity of the component needed per unit of the assembly (required)"`
	Reference     string  `json:"reference,omitempty" jsonschema:"Designators for this line, e.g. 'R1 R2 R3'"`
	Overage       string  `json:"overage,omitempty" jsonschema:"Allowance for wastage, either absolute ('5') or a percentage ('2%')"`
	Note          string  `json:"note,omitempty" jsonschema:"Free-text note for this BOM line"`
	Optional      *bool   `json:"optional,omitempty" jsonschema:"Line is optional and need not be allocated for a build to complete (default false)"`
	Consumable    *bool   `json:"consumable,omitempty" jsonschema:"Line is consumable and is not tracked through the build (default false)"`
	AllowVariants *bool   `json:"allow_variants,omitempty" jsonschema:"Stock of variant parts may be substituted for this component (default false)"`
	Inherited     *bool   `json:"inherited,omitempty" jsonschema:"Line is inherited by variants of this assembly (default false)"`
}

func RegisterAddBOMItem(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "add_bom_item",
		Description: "Add a component line to an assembly's bill of materials. " +
			"`part` is the assembly being built and `sub_part` is the component consumed -- getting these the wrong way round " +
			"silently creates a BOM on the wrong part, so confirm which is which before calling. " +
			"Quantity is per single unit of the assembly, not for the whole build. " +
			"The assembly part must have assembly=true; set it with update_part if InvenTree rejects the line.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddBOMItemInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Part == 0 || input.SubPart == 0 {
			return errResult(fmt.Errorf("part (the assembly) and sub_part (the component) are both required")), nil, nil
		}
		if input.Part == input.SubPart {
			return errResult(fmt.Errorf("part and sub_part are both %d: a part cannot be a component of itself", input.Part)), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than zero")), nil, nil
		}

		payload := map[string]any{
			"part":     input.Part,
			"sub_part": input.SubPart,
			"quantity": input.Quantity,
		}
		if input.Reference != "" {
			payload["reference"] = input.Reference
		}
		if input.Overage != "" {
			payload["overage"] = input.Overage
		}
		if input.Note != "" {
			payload["note"] = input.Note
		}
		if input.Optional != nil {
			payload["optional"] = *input.Optional
		}
		if input.Consumable != nil {
			payload["consumable"] = *input.Consumable
		}
		if input.AllowVariants != nil {
			payload["allow_variants"] = *input.AllowVariants
		}
		if input.Inherited != nil {
			payload["inherited"] = *input.Inherited
		}

		var created map[string]any
		if err := c.Post("/api/bom/", payload, &created); err != nil {
			return errResult(fmt.Errorf("adding BOM line (assembly %d, component %d): %w", input.Part, input.SubPart, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Update BOM Item --

type UpdateBOMItemInput struct {
	ID            int     `json:"id" jsonschema:"The BOM line ID (pk) to update. Get it from get_bom."`
	Quantity      float64 `json:"quantity,omitempty" jsonschema:"New quantity per unit of the assembly"`
	Reference     string  `json:"reference,omitempty" jsonschema:"New designators, e.g. 'R1 R2 R3'"`
	Overage       string  `json:"overage,omitempty" jsonschema:"New wastage allowance, absolute ('5') or percentage ('2%')"`
	Note          string  `json:"note,omitempty" jsonschema:"New note for this BOM line"`
	Optional      *bool   `json:"optional,omitempty" jsonschema:"Whether the line is optional"`
	Consumable    *bool   `json:"consumable,omitempty" jsonschema:"Whether the line is consumable"`
	AllowVariants *bool   `json:"allow_variants,omitempty" jsonschema:"Whether variant stock may be substituted"`
	Inherited     *bool   `json:"inherited,omitempty" jsonschema:"Whether the line is inherited by variants"`
}

func RegisterUpdateBOMItem(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "update_bom_item",
		Description: "Update an existing BOM line. Only the fields you supply are changed. " +
			"To swap which component a line uses, delete the line and add a new one rather than editing it in place.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateBOMItemInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		payload := map[string]any{}
		if input.Quantity > 0 {
			payload["quantity"] = input.Quantity
		}
		if input.Reference != "" {
			payload["reference"] = input.Reference
		}
		if input.Overage != "" {
			payload["overage"] = input.Overage
		}
		if input.Note != "" {
			payload["note"] = input.Note
		}
		if input.Optional != nil {
			payload["optional"] = *input.Optional
		}
		if input.Consumable != nil {
			payload["consumable"] = *input.Consumable
		}
		if input.AllowVariants != nil {
			payload["allow_variants"] = *input.AllowVariants
		}
		if input.Inherited != nil {
			payload["inherited"] = *input.Inherited
		}
		if len(payload) == 0 {
			return errResult(fmt.Errorf("no fields to update: supply at least one field to change")), nil, nil
		}

		var updated map[string]any
		if err := c.Patch(fmt.Sprintf("/api/bom/%d/", input.ID), payload, &updated); err != nil {
			return errResult(fmt.Errorf("updating BOM line %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Delete BOM Item --

type DeleteBOMItemInput struct {
	ID int `json:"id" jsonschema:"The BOM line ID (pk) to delete. Get it from get_bom."`
}

func RegisterDeleteBOMItem(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "delete_bom_item",
		Description: "Delete a line from an assembly's bill of materials. " +
			"This removes the component from the BOM; it does not touch existing build orders that already reference it.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteBOMItemInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}
		if err := c.Delete(fmt.Sprintf("/api/bom/%d/", input.ID)); err != nil {
			return errResult(fmt.Errorf("deleting BOM line %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("BOM line %d deleted.", input.ID))
	})
}
