package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Sales order status codes used by InvenTree (order/status_codes.py).
const (
	SOStatusPending    = 10
	SOStatusInProgress = 15
	SOStatusShipped    = 20
	SOStatusOnHold     = 25
	SOStatusComplete   = 30
	SOStatusCancelled  = 40
	SOStatusLost       = 50
	SOStatusReturned   = 60
)

// soSummary holds only the sales-order fields this package needs internally.
// Everything returned to the caller is decoded into map[string]any instead,
// so InvenTree's money-as-number fields and any added fields decode cleanly.
type soSummary struct {
	PK         int    `json:"pk"`
	Reference  string `json:"reference"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`
}

// -- List Sales Orders --

type ListSalesOrdersInput struct {
	Customer    int    `json:"customer,omitempty" jsonschema:"Filter by customer company ID. 0 or omit for all."`
	Status      int    `json:"status,omitempty" jsonschema:"Filter by status code: 10 pending, 15 in progress, 20 shipped, 25 on hold, 30 complete, 40 cancelled, 50 lost, 60 returned. 0 or omit for all."`
	Outstanding *bool  `json:"outstanding,omitempty" jsonschema:"Filter to orders that are still outstanding"`
	Search      string `json:"search,omitempty" jsonschema:"Search text matched against reference and description"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterListSalesOrders(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_sales_orders",
		Description: "List sales orders, optionally filtered by customer, status, or outstanding state. Use this to find an order ID before adding lines or shipping stock.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListSalesOrdersInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/order/so/?limit=%d&format=json", limit)
		if input.Customer != 0 {
			path += fmt.Sprintf("&customer=%d", input.Customer)
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
			return errResult(fmt.Errorf("listing sales orders: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Sales Order --

type GetSalesOrderInput struct {
	ID int `json:"id" jsonschema:"The sales order ID (pk)"`
}

func RegisterGetSalesOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_sales_order",
		Description: "Get a sales order with its line items and its shipments. " +
			"Line items show ordered, allocated and shipped quantities; shipments are the containers stock is allocated into before dispatch. " +
			"Call this to get both the line_item IDs and the shipment ID needed by allocate_sales_order_stock.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetSalesOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var order map[string]any
		if err := c.Get(fmt.Sprintf("/api/order/so/%d/?format=json", input.ID), &order); err != nil {
			return errResult(fmt.Errorf("getting sales order %d: %w", input.ID, err)), nil, nil
		}

		var lines client.PaginatedResponse[map[string]any]
		linePath := fmt.Sprintf("/api/order/so-line/?order=%d&limit=500&format=json", input.ID)
		if err := c.Get(linePath, &lines); err != nil {
			return errResult(fmt.Errorf("getting lines for sales order %d: %w", input.ID, err)), nil, nil
		}

		var shipments client.PaginatedResponse[map[string]any]
		shipPath := fmt.Sprintf("/api/order/so/shipment/?order=%d&limit=500&format=json", input.ID)
		if err := c.Get(shipPath, &shipments); err != nil {
			return errResult(fmt.Errorf("getting shipments for sales order %d: %w", input.ID, err)), nil, nil
		}

		return jsonResult(map[string]any{
			"order":     order,
			"lines":     lines.Results,
			"shipments": shipments.Results,
		})
	})
}

// -- Create Sales Order --

type CreateSalesOrderInput struct {
	Customer          int    `json:"customer" jsonschema:"Customer company ID (required). The company must have is_customer=true."`
	CustomerReference string `json:"customer_reference,omitempty" jsonschema:"The customer's own order number, e.g. their PO number. Put it here, not in reference."`
	Description       string `json:"description,omitempty" jsonschema:"Short description of the order"`
	Reference         string `json:"reference,omitempty" jsonschema:"Internal order reference. Omit to auto-generate the next in sequence, which is almost always what you want."`
	TargetDate        string `json:"target_date,omitempty" jsonschema:"Target shipping date, YYYY-MM-DD"`
	Notes             string `json:"notes,omitempty" jsonschema:"Free-text notes"`
}

func RegisterCreateSalesOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_sales_order",
		Description: "Create a sales order for a customer. The order is created in PENDING status with no lines; " +
			"add lines with add_sales_order_line, issue it with issue_sales_order, allocate stock into a shipment with " +
			"allocate_sales_order_stock, then dispatch it with ship_sales_order_shipment. " +
			"Leave `reference` empty so it is auto-generated -- InvenTree enforces a reference pattern and rejects " +
			"free-form strings. The customer's own order number goes in customer_reference.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateSalesOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Customer == 0 {
			return errResult(fmt.Errorf("customer is required")), nil, nil
		}

		reference := input.Reference
		if reference == "" {
			generated, err := nextReference(c, "/api/order/so/", "SO-")
			if err != nil {
				return errResult(err), nil, nil
			}
			reference = generated
		}

		description := input.Description
		if description == "" {
			description = input.CustomerReference
		}

		payload := map[string]any{
			"customer":    input.Customer,
			"reference":   reference,
			"description": description,
		}
		if input.CustomerReference != "" {
			payload["customer_reference"] = input.CustomerReference
		}
		if input.TargetDate != "" {
			payload["target_date"] = input.TargetDate
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}

		var created map[string]any
		if err := c.Post("/api/order/so/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating sales order (reference %q): %w", reference, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Add Sales Order Line --

type AddSalesOrderLineInput struct {
	Order      int     `json:"order" jsonschema:"Sales order ID (required)"`
	Part       int     `json:"part" jsonschema:"INTERNAL part ID being sold (required). Unlike purchase order lines, this is the internal part, not a supplier part."`
	Quantity   float64 `json:"quantity" jsonschema:"Quantity sold (required)"`
	SalePrice  string  `json:"sale_price,omitempty" jsonschema:"Unit sale price as a decimal string, e.g. '19.99'. Omit to use the part's default pricing."`
	Currency   string  `json:"currency,omitempty" jsonschema:"Currency code for sale_price, e.g. USD. Defaults to USD when a price is given."`
	Reference  string  `json:"reference,omitempty" jsonschema:"Line reference"`
	Notes      string  `json:"notes,omitempty" jsonschema:"Line notes"`
	TargetDate string  `json:"target_date,omitempty" jsonschema:"Target date for this line, YYYY-MM-DD"`
}

func RegisterAddSalesOrderLine(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "add_sales_order_line",
		Description: "Add a line item to a sales order. " +
			"The line references an INTERNAL part -- this is the opposite of add_purchase_order_line, which takes a supplier part. " +
			"Sale price is per unit; InvenTree computes the extended total.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AddSalesOrderLineInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 || input.Part == 0 {
			return errResult(fmt.Errorf("order and part are required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than zero")), nil, nil
		}

		payload := map[string]any{
			"order":    input.Order,
			"part":     input.Part,
			"quantity": input.Quantity,
		}
		if input.SalePrice != "" {
			currency := input.Currency
			if currency == "" {
				currency = "USD"
			}
			payload["sale_price"] = input.SalePrice
			payload["sale_price_currency"] = currency
		}
		if input.Reference != "" {
			payload["reference"] = input.Reference
		}
		if input.Notes != "" {
			payload["notes"] = input.Notes
		}
		if input.TargetDate != "" {
			payload["target_date"] = input.TargetDate
		}

		var created map[string]any
		if err := c.Post("/api/order/so-line/", payload, &created); err != nil {
			return errResult(fmt.Errorf("adding line to sales order %d: %w", input.Order, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Issue Sales Order --

type IssueSalesOrderInput struct {
	ID int `json:"id" jsonschema:"The sales order ID (pk) to issue"`
}

func RegisterIssueSalesOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "issue_sales_order",
		Description: "Issue a pending sales order, moving it from PENDING to IN PROGRESS. " +
			"Call this once the order is confirmed and ready to be fulfilled.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IssueSalesOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/order/so/%d/issue/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("issuing sales order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Sales order %d issued and is now IN PROGRESS.", input.ID))
	})
}

// -- Create Sales Order Shipment --

type CreateSalesOrderShipmentInput struct {
	Order          int    `json:"order" jsonschema:"Sales order ID (required)"`
	Reference      string `json:"reference,omitempty" jsonschema:"Shipment reference, e.g. '2' for the second shipment. Defaults to '1'."`
	TrackingNumber string `json:"tracking_number,omitempty" jsonschema:"Carrier tracking number"`
	InvoiceNumber  string `json:"invoice_number,omitempty" jsonschema:"Invoice number for this shipment"`
}

func RegisterCreateSalesOrderShipment(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_sales_order_shipment",
		Description: "Create a shipment against a sales order. Stock is allocated into a shipment, then the shipment is dispatched -- " +
			"so a shipment must exist before allocate_sales_order_stock can be called. " +
			"InvenTree usually creates a first shipment automatically with a new order, so check get_sales_order before creating another; " +
			"add extra shipments only for split or partial deliveries.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateSalesOrderShipmentInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 {
			return errResult(fmt.Errorf("order is required")), nil, nil
		}

		reference := input.Reference
		if reference == "" {
			reference = "1"
		}
		payload := map[string]any{
			"order":     input.Order,
			"reference": reference,
		}
		if input.TrackingNumber != "" {
			payload["tracking_number"] = input.TrackingNumber
		}
		if input.InvoiceNumber != "" {
			payload["invoice_number"] = input.InvoiceNumber
		}

		var created map[string]any
		if err := c.Post("/api/order/so/shipment/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating shipment for sales order %d: %w", input.Order, err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Allocate Sales Order Stock --

// SalesAllocation assigns stock to one sales order line within a shipment.
type SalesAllocation struct {
	LineItem  int     `json:"line_item" jsonschema:"Sales order LINE ITEM ID (required). Get it from get_sales_order."`
	StockItem int     `json:"stock_item" jsonschema:"Stock item ID to allocate from (required). Find it with get_stock filtered by part."`
	Quantity  float64 `json:"quantity" jsonschema:"Quantity to allocate from this stock item (required)"`
}

type AllocateSalesOrderStockInput struct {
	Order    int               `json:"order" jsonschema:"Sales order ID (required)"`
	Shipment int               `json:"shipment" jsonschema:"Shipment ID to allocate into (required). Get it from get_sales_order or create_sales_order_shipment."`
	Items    []SalesAllocation `json:"items" jsonschema:"Allocations to make (required)"`
}

func RegisterAllocateSalesOrderStock(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "allocate_sales_order_stock",
		Description: "Allocate stock items against a sales order's lines, reserving them within a specific shipment. " +
			"Allocation reserves stock but does not remove it from inventory -- that happens when the shipment is dispatched " +
			"with ship_sales_order_shipment. The order must be issued first.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AllocateSalesOrderStockInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Order == 0 {
			return errResult(fmt.Errorf("order is required")), nil, nil
		}
		if input.Shipment == 0 {
			return errResult(fmt.Errorf("shipment is required: allocate into a shipment, creating one with create_sales_order_shipment if needed")), nil, nil
		}
		if len(input.Items) == 0 {
			return errResult(fmt.Errorf("at least one item is required")), nil, nil
		}

		items := make([]map[string]any, 0, len(input.Items))
		for i, it := range input.Items {
			if it.LineItem == 0 || it.StockItem == 0 {
				return errResult(fmt.Errorf("items[%d]: line_item and stock_item are required", i)), nil, nil
			}
			if it.Quantity <= 0 {
				return errResult(fmt.Errorf("items[%d]: quantity must be greater than zero", i)), nil, nil
			}
			items = append(items, map[string]any{
				"line_item":  it.LineItem,
				"stock_item": it.StockItem,
				"quantity":   it.Quantity,
			})
		}

		payload := map[string]any{
			"shipment": input.Shipment,
			"items":    items,
		}

		var result any
		path := fmt.Sprintf("/api/order/so/%d/allocate/", input.Order)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("allocating stock to sales order %d: %w", input.Order, err)), nil, nil
		}
		return textResult(fmt.Sprintf(
			"Allocated %d item(s) into shipment %d on sales order %d. Dispatch it with ship_sales_order_shipment.",
			len(items), input.Shipment, input.Order))
	})
}

// -- Ship Sales Order Shipment --

type ShipSalesOrderShipmentInput struct {
	Shipment       int    `json:"shipment" jsonschema:"Shipment ID to dispatch (required)"`
	TrackingNumber string `json:"tracking_number,omitempty" jsonschema:"Carrier tracking number"`
	InvoiceNumber  string `json:"invoice_number,omitempty" jsonschema:"Invoice number for this shipment"`
	ShipmentDate   string `json:"shipment_date,omitempty" jsonschema:"Date shipped, YYYY-MM-DD. Defaults to today."`
}

func RegisterShipSalesOrderShipment(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "ship_sales_order_shipment",
		Description: "Dispatch a sales order shipment, removing the allocated stock from inventory. " +
			"This is the point at which stock actually leaves -- allocation alone does not reduce quantities. " +
			"Once every shipment is dispatched, close the order with complete_sales_order.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ShipSalesOrderShipmentInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Shipment == 0 {
			return errResult(fmt.Errorf("shipment is required")), nil, nil
		}

		payload := map[string]any{}
		if input.TrackingNumber != "" {
			payload["tracking_number"] = input.TrackingNumber
		}
		if input.InvoiceNumber != "" {
			payload["invoice_number"] = input.InvoiceNumber
		}
		if input.ShipmentDate != "" {
			payload["shipment_date"] = input.ShipmentDate
		}

		var result any
		path := fmt.Sprintf("/api/order/so/shipment/%d/ship/", input.Shipment)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("shipping shipment %d: %w", input.Shipment, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Shipment %d dispatched; allocated stock has been removed from inventory.", input.Shipment))
	})
}

// -- Complete Sales Order --

type CompleteSalesOrderInput struct {
	ID               int   `json:"id" jsonschema:"The sales order ID (pk) to complete"`
	AcceptIncomplete *bool `json:"accept_incomplete,omitempty" jsonschema:"Close the order even though some lines were never fully shipped (default false)"`
}

func RegisterCompleteSalesOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "complete_sales_order",
		Description: "Complete a sales order, moving it to COMPLETE. " +
			"Ship the shipments first -- by default InvenTree refuses to complete an order with unshipped allocations, " +
			"and accept_incomplete overrides that, so set it only when you genuinely intend to close the order short.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CompleteSalesOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		payload := map[string]any{
			"accept_incomplete": input.AcceptIncomplete != nil && *input.AcceptIncomplete,
		}

		var result any
		path := fmt.Sprintf("/api/order/so/%d/complete/", input.ID)
		if err := c.Post(path, payload, &result); err != nil {
			return errResult(fmt.Errorf("completing sales order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Sales order %d completed.", input.ID))
	})
}

// -- Cancel Sales Order --

type CancelSalesOrderInput struct {
	ID int `json:"id" jsonschema:"The sales order ID (pk) to cancel"`
}

func RegisterCancelSalesOrder(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "cancel_sales_order",
		Description: "Cancel a sales order, moving it to CANCELLED and releasing any stock allocated to it. " +
			"Stock that has already shipped is not returned; only outstanding allocations are released.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CancelSalesOrderInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.ID == 0 {
			return errResult(fmt.Errorf("id is required")), nil, nil
		}

		var order soSummary
		if err := c.Get(fmt.Sprintf("/api/order/so/%d/?format=json", input.ID), &order); err != nil {
			return errResult(fmt.Errorf("getting sales order %d: %w", input.ID, err)), nil, nil
		}
		switch order.Status {
		case SOStatusComplete, SOStatusCancelled:
			return errResult(fmt.Errorf("sales order %s is already %s (status %d), so it cannot be cancelled",
				order.Reference, order.StatusText, order.Status)), nil, nil
		}

		var result any
		path := fmt.Sprintf("/api/order/so/%d/cancel/", input.ID)
		if err := c.Post(path, map[string]any{}, &result); err != nil {
			return errResult(fmt.Errorf("cancelling sales order %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Sales order %s cancelled.", order.Reference))
	})
}
