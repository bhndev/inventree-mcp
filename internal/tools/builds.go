package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Build order status codes used by InvenTree (build/status_codes.py).
const (
	BuildStatusPending    = 10
	BuildStatusProduction = 20
	BuildStatusOnHold     = 25
	BuildStatusCancelled  = 30
	BuildStatusComplete   = 40
)

// buildSummary holds only the build-order fields this package needs internally.
// Everything returned to the caller is decoded into map[string]any instead, so
// InvenTree can change or add response fields without breaking these tools.
type buildSummary struct {
	PK         int    `json:"pk"`
	Reference  string `json:"reference"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`
	Part       int    `json:"part"`
}

// requireActiveBuild fails when a build order has already reached a terminal
// state, so finishing or cancelling one twice reports what actually happened
// instead of surfacing a bare 400 from the API. InvenTree allows these actions
// from any active state (pending, production or on hold), so only the terminal
// states are rejected here.
func requireActiveBuild(c *client.Client, id int, action string) error {
	var order buildSummary
	if err := c.Get(fmt.Sprintf("/api/build/%d/?format=json", id), &order); err != nil {
		return fmt.Errorf("getting build order %d: %w", id, err)
	}
	switch order.Status {
	case BuildStatusComplete, BuildStatusCancelled:
		return fmt.Errorf("build order %s is already %s (status %d), so it cannot be %s",
			order.Reference, order.StatusText, order.Status, action)
	}
	return nil
}

// -- List Build Orders --

type ListBuildOrdersInput struct {
	Part        int    `json:"part,omitempty" jsonschema:"Filter by the assembly part ID being built. 0 or omit for all."`
	Status      int    `json:"status,omitempty" jsonschema:"Filter by status code: 10 pending, 20 production, 25 on hold, 30 cancelled, 40 complete. 0 or omit for all."`
	Outstanding *bool  `json:"outstanding,omitempty" jsonschema:"Filter to builds that are still active (pending, production or on hold)"`
	Search      string `json:"search,omitempty" jsonschema:"Search text matched against reference, title and part name"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterListBuildOrders(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_build_orders",
		Description: "List build orders, optionally filtered by the assembly part being built, status, or outstanding state. Use this to find a build order ID before allocating stock or completing outputs.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListBuildOrdersInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/build/?limit=%d&format=json", limit)
		if input.Part != 0 {
			path += fmt.Sprintf("&part=%d", input.Part)
		}
		if input.Status != 0 {
			path += fmt.Sprintf("&status=%d", input.Status)
		}
		if input.Outstanding != nil {
			path += fmt.Sprintf("&outstanding=%t", *input.Outstanding)
		}
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}

		var resp client.PaginatedResponse[map[string]any]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing build orders: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Build Order --

type GetBuildOrderInput struct {
	ID int `json:"id" jsonschema:"The build order ID (pk)"`
}

func RegisterGetBuildOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_build_order",
		Description: "Get a build order together with its build lines -- one line per BOM component, showing the quantity required and how much has been allocated so far. " +
			"Call this before allocate_build_stock to get the build_line IDs, and to see what is still short.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetBuildOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var order map[string]any
		if err := c.Get(fmt.Sprintf("/api/build/%d/?format=json", input.ID), &order); err != nil {
			return errResult(fmt.Errorf("getting build order %d: %w", input.ID, err)), nil, nil
		}

		var lines client.PaginatedResponse[map[string]any]
		linePath := fmt.Sprintf("/api/build/line/?build=%d&limit=500&format=json", input.ID)
		if err := c.Get(linePath, &lines); err != nil {
			return errResult(fmt.Errorf("getting build lines for order %d: %w", input.ID, err)), nil, nil
		}

		return jsonResult(map[string]any{
			"order": order,
			"lines": lines.Results,
		})
	})
}

// -- Create Build Order --

type CreateBuildOrderInput struct {
	Part        int     `json:"part" jsonschema:"Part ID of the ASSEMBLY to build (required). The part must have assembly=true and a BOM."`
	Quantity    float64 `json:"quantity" jsonschema:"Number of units to build (required)"`
	Title       string  `json:"title,omitempty" jsonschema:"Short description of the build. Omit to generate one from the part name."`
	Reference   string  `json:"reference,omitempty" jsonschema:"Internal build reference. Omit to auto-generate the next in sequence, which is almost always what you want."`
	TargetDate  string  `json:"target_date,omitempty" jsonschema:"Target completion date, YYYY-MM-DD"`
	TakeFrom    int     `json:"take_from,omitempty" jsonschema:"Stock location ID to source components from. 0 or omit to allow any location."`
	Destination int     `json:"destination,omitempty" jsonschema:"Stock location ID where completed outputs should land. 0 or omit for none."`
	Batch       string  `json:"batch,omitempty" jsonschema:"Batch code for the build outputs"`
	SalesOrder  int     `json:"sales_order,omitempty" jsonschema:"Sales order ID this build fulfils. 0 or omit for none."`
	Notes       string  `json:"notes,omitempty" jsonschema:"Free-text notes"`
}

func RegisterCreateBuildOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_build_order",
		Description: "Create a build order to manufacture an assembly from its BOM. The order is created in PENDING status; " +
			"issue it with issue_build_order, allocate components with allocate_build_stock, create outputs with create_build_output, " +
			"then finish it with finish_build_order. " +
			"The part must be marked as an assembly and have BOM lines -- check with get_bom first. " +
			"Leave `reference` empty so it is auto-generated; InvenTree enforces a reference pattern and rejects free-form strings.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateBuildOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than zero")), nil, nil
		}

		// InvenTree requires a non-blank title. Derive one from the part rather
		// than making the caller supply boilerplate, and use the lookup to fail
		// early with a clear message if the part is not an assembly.
		title := input.Title
		if title == "" {
			var part map[string]any
			if err := c.Get(fmt.Sprintf("/api/part/%d/?format=json", input.Part), &part); err != nil {
				return errResult(fmt.Errorf("getting part %d to derive a build title: %w", input.Part, err)), nil, nil
			}
			if assembly, ok := part["assembly"].(bool); ok && !assembly {
				return errResult(fmt.Errorf(
					"part %d is not marked as an assembly, so it cannot be built; set assembly=true with update_part first",
					input.Part)), nil, nil
			}
			name, _ := part["name"].(string)
			if name == "" {
				name = fmt.Sprintf("part %d", input.Part)
			}
			title = fmt.Sprintf("Build %s", name)
		}

		reference := input.Reference
		if reference == "" {
			generated, err := nextReference(c, "/api/build/", "BO-")
			if err != nil {
				return errResult(err), nil, nil
			}
			reference = generated
		}

		payload := map[string]any{
			"part":      input.Part,
			"quantity":  input.Quantity,
			"reference": reference,
			"title":     title,
		}
		if input.TargetDate != "" {
			payload["target_date"] = input.TargetDate
		}
		if input.TakeFrom != 0 {
			payload["take_from"] = input.TakeFrom
		}
		if input.Destination != 0 {
			payload["destination"] = input.Destination
		}
		if input.Batch != "" {
			payload["batch"] = input.Batch
		}
		if input.SalesOrder != 0 {
			payload["sales_order"] = input.SalesOrder
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created map[string]any
		if err := c.Post("/api/build/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating build order (reference %q): %w", reference, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Issue Build Order --

type IssueBuildOrderInput struct {
	ID int `json:"id" jsonschema:"The build order ID (pk) to issue"`
}

func RegisterIssueBuildOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "issue_build_order",
		Description: "Issue a pending build order, moving it from PENDING to PRODUCTION. " +
			"Call this once the build is actually starting on the floor.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IssueBuildOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/build/%d/issue/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("issuing build order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Build order %d issued and is now in PRODUCTION.", input.ID))
	})
}

// -- Allocate Build Stock --

// BuildAllocation is one component allocation against a build line.
type BuildAllocation struct {
	BuildLine int     `json:"build_line" jsonschema:"BUILD LINE ID (required), not the part ID. Get it from get_build_order."`
	StockItem int     `json:"stock_item" jsonschema:"Stock item ID to allocate from (required). Find it with get_stock filtered by part."`
	Quantity  float64 `json:"quantity" jsonschema:"Quantity to allocate from this stock item (required)"`
	Output    int     `json:"output,omitempty" jsonschema:"Build output stock item ID, required only for trackable components. 0 or omit for untracked."`
}

type AllocateBuildStockInput struct {
	Build int               `json:"build" jsonschema:"Build order ID (required)"`
	Items []BuildAllocation `json:"items" jsonschema:"Allocations to make (required). Allocate several lines in one call."`
}

func RegisterAllocateBuildStock(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "allocate_build_stock",
		Description: "Allocate specific stock items to a build order's component lines, reserving them for the build. " +
			"Each item pairs a build_line (from get_build_order) with a stock_item and quantity -- note it takes the build LINE id, not a part id. " +
			"Use auto_allocate_build_stock instead when you just want InvenTree to pick suitable stock itself. " +
			"Allocation reserves stock; it is consumed only when outputs are completed.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AllocateBuildStockInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Build == 0 {
			return errResult(fmt.Errorf("build is required")), nil, nil
		}
		if len(input.Items) == 0 {
			return errResult(fmt.Errorf("at least one item is required")), nil, nil
		}

		items := make([]map[string]any, 0, len(input.Items))
		for i, it := range input.Items {
			if it.BuildLine == 0 || it.StockItem == 0 {
				return errResult(fmt.Errorf("items[%d]: build_line and stock_item are required", i)), nil, nil
			}
			if it.Quantity <= 0 {
				return errResult(fmt.Errorf("items[%d]: quantity must be greater than zero", i)), nil, nil
			}
			entry := map[string]any{
				"build_line": it.BuildLine,
				"stock_item": it.StockItem,
				"quantity":   it.Quantity,
			}
			if it.Output != 0 {
				entry["output"] = it.Output
			}
			items = append(items, entry)
		}

		var result any
		path := fmt.Sprintf("/api/build/%d/allocate/", input.Build)
		if err := c.Post(path, map[string]any{"items": items}, &result); err != nil {
			return errResult(fmt.Errorf("allocating stock to build %d: %w", input.Build, err)), nil, nil
		}
		return textResult(fmt.Sprintf(
			"Allocated %d item(s) to build order %d. Call get_build_order to see updated allocation quantities.",
			len(items), input.Build))
	})
}

// -- Auto Allocate Build Stock --

type AutoAllocateBuildStockInput struct {
	Build           int   `json:"build" jsonschema:"Build order ID (required)"`
	Location        int   `json:"location,omitempty" jsonschema:"Restrict allocation to stock in this location ID. 0 or omit for any location."`
	ExcludeLocation int   `json:"exclude_location,omitempty" jsonschema:"Exclude stock in this location ID. 0 or omit to exclude nothing."`
	Interchangeable *bool `json:"interchangeable,omitempty" jsonschema:"Treat stock items in different locations as interchangeable (default false)"`
	Substitutes     *bool `json:"substitutes,omitempty" jsonschema:"Allow substitute parts to be allocated (default true)"`
	OptionalItems   *bool `json:"optional_items,omitempty" jsonschema:"Also allocate BOM lines marked optional (default false)"`
}

func RegisterAutoAllocateBuildStock(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "auto_allocate_build_stock",
		Description: "Let InvenTree automatically allocate available stock to a build order's untracked component lines. " +
			"This is the quick path when you do not care which specific stock items are used. " +
			"It only handles untracked components -- trackable ones must still be allocated per output with allocate_build_stock. " +
			"Call get_build_order afterwards to see what it managed to allocate and what is still short.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AutoAllocateBuildStockInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Build == 0 {
			return errResult(fmt.Errorf("build is required")), nil, nil
		}

		payload := map[string]any{
			"interchangeable": input.Interchangeable != nil && *input.Interchangeable,
			"substitutes":     input.Substitutes == nil || *input.Substitutes,
			"optional_items":  input.OptionalItems != nil && *input.OptionalItems,
		}
		if input.Location != 0 {
			payload["location"] = input.Location
		}
		if input.ExcludeLocation != 0 {
			payload["exclude_location"] = input.ExcludeLocation
		}

		var result any
		path := fmt.Sprintf("/api/build/%d/auto-allocate/", input.Build)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("auto-allocating stock to build %d: %w", input.Build, err)), nil, nil
		}
		return textResult(fmt.Sprintf(
			"Auto-allocation requested for build order %d. Call get_build_order to see which lines were filled and which are still short.",
			input.Build))
	})
}

// -- Create Build Output --

type CreateBuildOutputInput struct {
	Build         int     `json:"build" jsonschema:"Build order ID (required)"`
	Quantity      float64 `json:"quantity" jsonschema:"Number of units in this output (required)"`
	SerialNumbers string  `json:"serial_numbers,omitempty" jsonschema:"Serial numbers for trackable assemblies, e.g. '1-10' or '1,2,5'. Required when the assembly part is trackable."`
	BatchCode     string  `json:"batch_code,omitempty" jsonschema:"Batch code for this output"`
	AutoAllocate  *bool   `json:"auto_allocate,omitempty" jsonschema:"Automatically allocate matching trackable stock to this output (default false)"`
}

func RegisterCreateBuildOutput(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_build_output",
		Description: "Create a build output -- the in-progress unit(s) being assembled against a build order. " +
			"Outputs are created incomplete; complete them with complete_build_outputs, which consumes the allocated components and turns the output into real stock. " +
			"If the assembly part is trackable you must supply serial_numbers, one per unit.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateBuildOutputInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Build == 0 {
			return errResult(fmt.Errorf("build is required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than zero")), nil, nil
		}

		payload := map[string]any{
			"quantity":      input.Quantity,
			"auto_allocate": input.AutoAllocate != nil && *input.AutoAllocate,
		}
		if input.SerialNumbers != "" {
			payload["serial_numbers"] = input.SerialNumbers
		}
		if input.BatchCode != "" {
			payload["batch_code"] = input.BatchCode
		}

		// This endpoint returns an ARRAY of created stock items rather than a
		// single object, so decode into `any`.
		var result any
		path := fmt.Sprintf("/api/build/%d/create-output/", input.Build)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("creating output for build %d: %w", input.Build, err)), nil, nil
		}
		return jsonResult(result)
	})
}

// -- Complete Build Outputs --

// BuildOutputRef identifies one build output being completed.
type BuildOutputRef struct {
	Output int `json:"output" jsonschema:"Build output stock item ID (required). Get it from get_stock filtered by the assembly part, or from create_build_output."`
}

type CompleteBuildOutputsInput struct {
	Build    int              `json:"build" jsonschema:"Build order ID (required)"`
	Outputs  []BuildOutputRef `json:"outputs" jsonschema:"Build outputs to complete (required)"`
	Location int              `json:"location,omitempty" jsonschema:"Stock location ID for the completed stock. 0 or omit to use the build order's destination."`
	Status   int              `json:"status,omitempty" jsonschema:"Stock status code: 10 OK (default), 50 attention needed, 55 damaged, 65 rejected, 75 quarantined."`
	Notes    string           `json:"notes,omitempty" jsonschema:"Notes recorded against the completion"`
}

func RegisterCompleteBuildOutputs(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "complete_build_outputs",
		Description: "Complete one or more build outputs, consuming their allocated components and turning the outputs into finished stock. " +
			"This completes OUTPUTS, not the build order itself -- use finish_build_order to close the order once all outputs are done. " +
			"Components must be allocated first or completion fails.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CompleteBuildOutputsInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Build == 0 {
			return errResult(fmt.Errorf("build is required")), nil, nil
		}
		if len(input.Outputs) == 0 {
			return errResult(fmt.Errorf("at least one output is required")), nil, nil
		}

		outputs := make([]map[string]any, 0, len(input.Outputs))
		for i, o := range input.Outputs {
			if o.Output == 0 {
				return errResult(fmt.Errorf("outputs[%d]: output is required", i)), nil, nil
			}
			outputs = append(outputs, map[string]any{"output": o.Output})
		}

		status := input.Status
		if status == 0 {
			status = 10 // OK
		}
		payload := map[string]any{
			"outputs": outputs,
			"status":  status,
		}
		if input.Location != 0 {
			payload["location"] = input.Location
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var result any
		path := fmt.Sprintf("/api/build/%d/complete/", input.Build)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("completing outputs for build %d: %w", input.Build, err)), nil, nil
		}
		return textResult(fmt.Sprintf(
			"Completed %d build output(s) against build order %d. Call finish_build_order to close the order once every output is done.",
			len(outputs), input.Build))
	})
}

// -- Finish Build Order --

type FinishBuildOrderInput struct {
	ID                 int    `json:"id" jsonschema:"The build order ID (pk) to finish"`
	AcceptUnallocated  *bool  `json:"accept_unallocated,omitempty" jsonschema:"Close the order even though some components were never allocated (default false)"`
	AcceptIncomplete   *bool  `json:"accept_incomplete,omitempty" jsonschema:"Close the order even though fewer outputs were completed than ordered (default false)"`
	AcceptOverallocted string `json:"accept_overallocated,omitempty" jsonschema:"How to handle stock allocated beyond what was needed: 'accept' to consume it, 'trim' to return the excess, 'cancel' to refuse. Defaults to 'cancel'."`
}

func RegisterFinishBuildOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "finish_build_order",
		Description: "Finish (complete) a build order, moving it to COMPLETE. " +
			"This closes the ORDER; complete the individual outputs first with complete_build_outputs. " +
			"By default InvenTree refuses to finish an order with unallocated components, incomplete outputs, or over-allocated stock -- " +
			"the accept_* options override each of those, so only set them when you genuinely intend to close the order short.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input FinishBuildOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}
		if err := requireActiveBuild(c, input.ID, "finished"); err != nil {
			return errResult(err), nil, nil
		}

		payload := map[string]any{
			"accept_unallocated": input.AcceptUnallocated != nil && *input.AcceptUnallocated,
			"accept_incomplete":  input.AcceptIncomplete != nil && *input.AcceptIncomplete,
		}
		if input.AcceptOverallocted != "" {
			payload["accept_overallocated"] = input.AcceptOverallocted
		}

		var result any
		path := fmt.Sprintf("/api/build/%d/finish/", input.ID)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("finishing build order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Build order %d finished and is now COMPLETE.", input.ID))
	})
}

// -- Cancel Build Order --

type CancelBuildOrderInput struct {
	ID                      int   `json:"id" jsonschema:"The build order ID (pk) to cancel"`
	RemoveAllocatedStock    *bool `json:"remove_allocated_stock,omitempty" jsonschema:"Consume (remove) stock already allocated to this build rather than returning it (default false)"`
	RemoveIncompleteOutputs *bool `json:"remove_incomplete_outputs,omitempty" jsonschema:"Delete any build outputs that were never completed (default false)"`
}

func RegisterCancelBuildOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "cancel_build_order",
		Description: "Cancel a build order, moving it to CANCELLED. " +
			"By default allocated stock is returned to inventory and incomplete outputs are left alone; the remove_* options change that. " +
			"A build order can only be deleted after it has been cancelled.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CancelBuildOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}
		if err := requireActiveBuild(c, input.ID, "cancelled"); err != nil {
			return errResult(err), nil, nil
		}

		payload := map[string]any{
			"remove_allocated_stock":    input.RemoveAllocatedStock != nil && *input.RemoveAllocatedStock,
			"remove_incomplete_outputs": input.RemoveIncompleteOutputs != nil && *input.RemoveIncompleteOutputs,
		}

		var result any
		path := fmt.Sprintf("/api/build/%d/cancel/", input.ID)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("cancelling build order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Build order %d cancelled.", input.ID))
	})
}
