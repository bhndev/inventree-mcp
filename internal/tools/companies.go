package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Company represents an InvenTree company (supplier, manufacturer, customer).
// A single company may fill more than one role.
type Company struct {
	PK             int    `json:"pk"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Website        string `json:"website"`
	IsSupplier     bool   `json:"is_supplier"`
	IsManufacturer bool   `json:"is_manufacturer"`
	IsCustomer     bool   `json:"is_customer"`
	Currency       string `json:"currency"`
	Active         bool   `json:"active"`
}

// -- Search Companies --

type SearchCompaniesInput struct {
	Search         string `json:"search,omitempty" jsonschema:"Search query to find companies by name. Omit to list all."`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Filter to companies flagged as suppliers"`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Filter to companies flagged as manufacturers"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 25)"`
}

func RegisterSearchCompanies(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "search_companies",
		Description: "Search for companies (suppliers, manufacturers, customers) by name. Always search before creating a company, so vendors are not duplicated with slight spelling differences.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCompaniesInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/company/?limit=%d&format=json", limit)
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}
		if input.IsSupplier != nil {
			path += fmt.Sprintf("&is_supplier=%t", *input.IsSupplier)
		}
		if input.IsManufacturer != nil {
			path += fmt.Sprintf("&is_manufacturer=%t", *input.IsManufacturer)
		}

		var resp client.PaginatedResponse[Company]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching companies: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Create Company --

type CreateCompanyInput struct {
	Name           string `json:"name" jsonschema:"Company name (required)"`
	Description    string `json:"description,omitempty" jsonschema:"Short description. Defaults to the name if omitted."`
	Website        string `json:"website,omitempty" jsonschema:"Company website URL"`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Company supplies parts to us (default true)"`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Company manufactures parts (default false)"`
	IsCustomer     *bool  `json:"is_customer,omitempty" jsonschema:"Company buys from us (default false)"`
	Currency       string `json:"currency,omitempty" jsonschema:"Default currency code, e.g. USD"`
}

func RegisterCreateCompany(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_company",
		Description: "Create a company: a supplier, a manufacturer, or both. Search first with search_companies to avoid duplicates. " +
			"A supplier is who you buy from (Digikey, Mouser); a manufacturer is who makes the part (Molex, JST). " +
			"The same company can be both, in which case set both flags on one record rather than creating two.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateCompanyInput) (*mcp.CallToolResult, any, error) {
		c := callerClient(c, req)
		if input.Name == "" {
			return errResult(fmt.Errorf("name is required")), nil, nil
		}

		description := input.Description
		if description == "" {
			description = input.Name
		}

		isSupplier := true
		if input.IsSupplier != nil {
			isSupplier = *input.IsSupplier
		}

		payload := map[string]any{
			"name":            input.Name,
			"description":     description,
			"is_supplier":     isSupplier,
			"is_manufacturer": input.IsManufacturer != nil && *input.IsManufacturer,
			"is_customer":     input.IsCustomer != nil && *input.IsCustomer,
		}
		if input.Website != "" {
			payload["website"] = input.Website
		}
		if input.Currency != "" {
			payload["currency"] = input.Currency
		}

		var created Company
		if err := c.Post("/api/company/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating company: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}
