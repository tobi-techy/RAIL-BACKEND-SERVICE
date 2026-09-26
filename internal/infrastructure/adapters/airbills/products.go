package airbills

import "encoding/json"

// flattenProducts turns the live /list payloads into the flat options Miriam
// shows. Electricity, cable, data, and betting nest their rows; transport is
// already an array. networkID keeps only that carrier when the data catalogue
// returns every network at once.
func flattenProducts(raw json.RawMessage, networkID string) []Product {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var asList []Product
	if err := json.Unmarshal(raw, &asList); err == nil && len(asList) > 0 && (asList[0].ProdID != "" || asList[0].Name != "") {
		return asList
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	var out []Product
	for _, key := range []string{"ELECTRIC_COMPANY", "bettingCompany"} {
		out = append(out, decodeRows(obj[key], networkID)...)
	}
	if nested, ok := obj["CableTv"]; ok {
		out = append(out, decodeGrouped(nested, networkID)...)
	}
	if nested, ok := obj["dataPlan"]; ok {
		out = append(out, decodeGrouped(nested, networkID)...)
	}
	return out
}

func decodeGrouped(raw json.RawMessage, networkID string) []Product {
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return decodeRows(raw, networkID)
	}
	var out []Product
	for _, rows := range groups {
		out = append(out, decodeRows(rows, networkID)...)
	}
	return out
}

func decodeRows(raw json.RawMessage, networkID string) []Product {
	if len(raw) == 0 {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil
	}
	out := make([]Product, 0, len(rows))
	for _, row := range rows {
		item := Product{
			ProdID:     firstString(row, "prodId", "prodID"),
			Name:       firstString(row, "name", "prodName", "electName"),
			Amount:     firstFloat(row, "amount", "prodAmount"),
			ProdAmount: firstFloat(row, "prodAmount"),
			ElectID:    firstString(row, "electId"),
			NetworkID:  firstString(row, "networkId", "network_id"),
		}
		if item.Amount == 0 {
			item.Amount = item.ProdAmount
		}
		if networkID != "" && item.NetworkID != "" && item.NetworkID != networkID {
			continue
		}
		if item.ProdID == "" && item.Name == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func firstString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := row[key].(type) {
		case string:
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func firstFloat(row map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch v := row[key].(type) {
		case float64:
			if v != 0 {
				return v
			}
		}
	}
	return 0
}
