package tools

import (
	"context"

	"github.com/google/uuid"
	"github.com/rail-service/rail_service/internal/domain/services/ai/core"
)

// RegisterBillTools registers Nigerian bill-payment tools (airtime, data,
// electricity, cable TV, betting, transport) backed by Airbills. Read tools run
// directly; pay_bill/automate_bill/save_bill_beneficiary are action tools staged
// for confirmation (fund-moving ones require Face ID step-up).
func RegisterBillTools(r *Registry) {
	r.Register(NewTool(
		"list_bill_providers",
		"List available providers/plans for a Nigerian bill category. category is one of: electricity, cable, betting, transport. Use before paying electricity (to get the disco/elect_id), cable, betting or transport bills.",
		SimpleArgs(map[string]map[string]interface{}{
			"category": EnumParam("Bill category", []string{"electricity", "cable", "betting", "transport"}),
		}, []string{"category"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			list, err := deps.Bills.ListProviders(ctx, GetArgString(args, "category"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"providers": list}}, nil
		},
	))

	r.Register(NewTool(
		"get_data_plans",
		"List mobile data bundles, optionally scoped to a network (01=MTN, 02=Glo, 03=9mobile, 04=Airtel). Returns prod_id, name and price used by pay_bill for data.",
		SimpleArgs(map[string]map[string]interface{}{
			"network_id": StringParam("Network id: 01=MTN, 02=Glo, 03=9mobile, 04=Airtel (optional)"),
		}, nil),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			plans, err := deps.Bills.GetDataPlans(ctx, GetArgString(args, "network_id"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"plans": plans}}, nil
		},
	))

	r.Register(NewTool(
		"get_cable_packages",
		"List cable-TV bouquets (DStv, GOtv, Startimes). Returns prod_id, name and price used by pay_bill for cable.",
		SimpleArgs(nil, nil),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			pkgs, err := deps.Bills.GetCablePackages(ctx)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"packages": pkgs}}, nil
		},
	))

	r.Register(NewTool(
		"validate_meter",
		"Validate an electricity meter number for a disco and return the account holder name. Always do this before paying an electricity bill so the user can confirm the name.",
		SimpleArgs(map[string]map[string]interface{}{
			"meter_no": StringParam("Electricity meter number"),
			"elect_id": StringParam("Disco/electricity provider id from list_bill_providers"),
		}, []string{"meter_no", "elect_id"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			res, err := deps.Bills.ValidateMeter(ctx, GetArgString(args, "meter_no"), GetArgString(args, "elect_id"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"detect_network",
		"Detect the Nigerian mobile network for a phone number. Returns network_id (01=MTN, 02=Glo, 03=9mobile, 04=Airtel) and name.",
		SimpleArgs(map[string]map[string]interface{}{
			"phone": StringParam("Phone number in local format, e.g. 08012345678"),
		}, []string{"phone"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			res, err := deps.Bills.DetectNetwork(ctx, GetArgString(args, "phone"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"list_bill_beneficiaries",
		"List the user's saved bill-payment beneficiaries (people/meters/cards they pay regularly), optionally filtered by category.",
		SimpleArgs(map[string]map[string]interface{}{
			"category": StringParam("Optional filter: airtime, data, electricity, cable, betting, transport"),
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			list, err := deps.Bills.ListBeneficiaries(ctx, userID, GetArgString(args, "category"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"beneficiaries": list}}, nil
		},
	))

	r.Register(NewTool(
		"get_bill_payment_history",
		"Show the user's recent bill payments (airtime, data, electricity, etc.) with amounts and status.",
		SimpleArgs(map[string]map[string]interface{}{
			"limit": NumberParam("How many to return (1-50, default 20)"),
		}, nil),
		core.CategoryHistory,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			limit := int(GetArgFloat(args, "limit"))
			history, err := deps.Bills.GetPaymentHistory(ctx, userID, limit)
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: map[string]interface{}{"history": history}}, nil
		},
	))

	// --- Action tools (staged for confirmation) ---

	r.Register(NewTool(
		"pay_bill",
		"Pay a Nigerian bill now: airtime, data, electricity, cable TV, betting or transport. For data/cable pass prod_id from get_data_plans/get_cable_packages; for electricity pass meter number as recipient and elect_id, and validate_meter first. amount_ngn is the face value in Naira. This moves money and requires user confirmation.",
		SimpleArgs(map[string]map[string]interface{}{
			"category":   EnumParam("Bill category", []string{"airtime", "data", "electricity", "cable", "betting", "transport"}),
			"recipient":  StringParam("Phone number, meter number, smartcard number or betting/customer id"),
			"amount_ngn": NumberParam("Amount in Naira (face value)"),
			"network_id": StringParam("For airtime/data: 01=MTN, 02=Glo, 03=9mobile, 04=Airtel (auto-detected if omitted)"),
			"prod_id":    StringParam("Plan/package id for data or cable (from lookups)"),
			"elect_id":   StringParam("Disco id for electricity (from list_bill_providers)"),
		}, []string{"category", "recipient", "amount_ngn"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			res, err := deps.Bills.PayBill(ctx, userID,
				GetArgString(args, "category"), GetArgString(args, "recipient"),
				GetArgString(args, "network_id"), GetArgString(args, "prod_id"),
				GetArgString(args, "elect_id"), GetArgFloat(args, "amount_ngn"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"automate_bill",
		"Set up a recurring auto-pay for a Nigerian bill (e.g. monthly data, weekly airtime, electricity). Same fields as pay_bill plus a schedule. Runs automatically under the user's bill-pay mandate. This authorizes recurring money movement and requires user confirmation.",
		SimpleArgs(map[string]map[string]interface{}{
			"category":   EnumParam("Bill category", []string{"airtime", "data", "electricity", "cable", "betting", "transport"}),
			"recipient":  StringParam("Phone number, meter number, smartcard number or betting/customer id"),
			"amount_ngn": NumberParam("Amount in Naira (face value)"),
			"schedule":   StringParam("Recurrence, e.g. 'monthly', 'weekly', or a day like 'monthly on the 1st'"),
			"network_id": StringParam("For airtime/data: 01=MTN, 02=Glo, 03=9mobile, 04=Airtel"),
			"prod_id":    StringParam("Plan/package id for data or cable"),
			"elect_id":   StringParam("Disco id for electricity"),
		}, []string{"category", "recipient", "amount_ngn", "schedule"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			res, err := deps.Bills.AutomateBill(ctx, userID,
				GetArgString(args, "category"), GetArgString(args, "recipient"),
				GetArgString(args, "network_id"), GetArgString(args, "prod_id"),
				GetArgString(args, "elect_id"), GetArgFloat(args, "amount_ngn"),
				GetArgString(args, "schedule"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))

	r.Register(NewTool(
		"save_bill_beneficiary",
		"Save a bill-payment beneficiary so the user can pay them again easily (e.g. \"Mum's airtime\", a specific meter or smartcard). Does not move money.",
		SimpleArgs(map[string]map[string]interface{}{
			"label":      StringParam("Friendly name, e.g. \"Mum's phone\""),
			"category":   EnumParam("Bill category", []string{"airtime", "data", "electricity", "cable", "betting", "transport"}),
			"recipient":  StringParam("Phone number, meter number, smartcard number or betting/customer id"),
			"network_id": StringParam("For airtime/data (optional)"),
			"prod_id":    StringParam("Default plan/package id (optional)"),
			"elect_id":   StringParam("Disco id for electricity (optional)"),
		}, []string{"label", "category", "recipient"}),
		core.CategoryAction,
		func(ctx context.Context, userID uuid.UUID, args map[string]interface{}, deps *core.Dependencies) (*core.ToolResult, error) {
			if deps.Bills == nil {
				return &core.ToolResult{Error: "bill payments not available"}, nil
			}
			res, err := deps.Bills.SaveBeneficiary(ctx, userID,
				GetArgString(args, "label"), GetArgString(args, "category"),
				GetArgString(args, "recipient"), GetArgString(args, "network_id"),
				GetArgString(args, "prod_id"), GetArgString(args, "elect_id"))
			if err != nil {
				return &core.ToolResult{Error: err.Error()}, nil
			}
			return &core.ToolResult{Data: res}, nil
		},
	))
}
