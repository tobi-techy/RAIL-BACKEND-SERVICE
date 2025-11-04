package pagination

import (
	"fmt"
	"strings"
)

// Cursor represents a pagination cursor
type Cursor struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at,omitempty"`
	SortValue string `json:"sort_value,omitempty"`
}

// Encode encodes cursor to string
func (c *Cursor) Encode() string {
	if c.SortValue != "" {
		return c.ID + ":" + c.SortValue
	}
	return c.ID
}

// DecodeCursor decodes cursor from string
func DecodeCursor(cursorStr string) (*Cursor, error) {
	if cursorStr == "" {
		return nil, nil
	}

	parts := strings.Split(cursorStr, ":")
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid cursor format")
	}

	cursor := &Cursor{ID: parts[0]}
	if len(parts) > 1 {
		cursor.SortValue = parts[1]
	}

	return cursor, nil
}

// PageInfo contains pagination information
type PageInfo struct {
	HasNextPage     bool    `json:"hasNextPage"`
	HasPreviousPage bool    `json:"hasPreviousPage"`
	StartCursor     *string `json:"startCursor,omitempty"`
	EndCursor       *string `json:"endCursor,omitempty"`
	TotalCount      *int    `json:"totalCount,omitempty"`
}

// Connection represents a paginated connection
type Connection struct {
	Edges    interface{} `json:"edges"`
	PageInfo *PageInfo   `json:"pageInfo"`
}

// Edge represents a single edge in a connection
type Edge struct {
	Node   interface{} `json:"node"`
	Cursor string      `json:"cursor"`
}

// Params contains pagination parameters
type Params struct {
	First  *int    `json:"first,omitempty"`
	Last   *int    `json:"last,omitempty"`
	After  *string `json:"after,omitempty"`
	Before *string `json:"before,omitempty"`
}

// Validate validates pagination parameters
func (p *Params) Validate() error {
	if p.First != nil && p.Last != nil {
		return fmt.Errorf("cannot specify both first and last")
	}

	if p.After != nil && p.Before != nil {
		return fmt.Errorf("cannot specify both after and before")
	}

	if p.First != nil && *p.First < 0 {
		return fmt.Errorf("first cannot be negative")
	}

	if p.Last != nil && *p.Last < 0 {
		return fmt.Errorf("last cannot be negative")
	}

	const maxLimit = 100
	if p.First != nil && *p.First > maxLimit {
		*p.First = maxLimit
	}

	if p.Last != nil && *p.Last > maxLimit {
		*p.Last = maxLimit
	}

	return nil
}

// GetLimit returns the limit for the query
func (p *Params) GetLimit() int {
	if p.First != nil {
		return *p.First + 1 // +1 to check if there are more results
	}
	if p.Last != nil {
		return *p.Last + 1 // +1 to check if there are more results
	}
	return 51 // Default limit + 1
}

// GetOffset returns the offset for the query (for backward compatibility)
func (p *Params) GetOffset() int {
	// This is a simplified implementation
	// In a full implementation, you'd decode cursors and calculate proper offsets
	return 0
}

// BuildQuery builds a paginated query
func (p *Params) BuildQuery(baseQuery string, cursorField string, idField string) (string, []interface{}, error) {
	args := []interface{}{}
	query := baseQuery

	// Add cursor conditions
	if p.After != nil {
		cursor, err := DecodeCursor(*p.After)
		if err != nil {
			return "", nil, err
		}
		if cursor != nil {
			if cursor.SortValue != "" {
				query += fmt.Sprintf(" AND (%s > $%d OR (%s = $%d AND %s > $%d))",
					cursorField, len(args)+1, cursorField, len(args)+1, idField, len(args)+2)
				args = append(args, cursor.SortValue, cursor.ID)
			} else {
				query += fmt.Sprintf(" AND %s > $%d", cursorField, len(args)+1)
				args = append(args, cursor.ID)
			}
		}
	}

	if p.Before != nil {
		cursor, err := DecodeCursor(*p.Before)
		if err != nil {
			return "", nil, err
		}
		if cursor != nil {
			if cursor.SortValue != "" {
				query += fmt.Sprintf(" AND (%s < $%d OR (%s = $%d AND %s < $%d))",
					cursorField, len(args)+1, cursorField, len(args)+1, idField, len(args)+2)
				args = append(args, cursor.SortValue, cursor.ID)
			} else {
				query += fmt.Sprintf(" AND %s < $%d", cursorField, len(args)+1)
				args = append(args, cursor.ID)
			}
		}
	}

	// Add ordering
	if p.Last != nil {
		query += fmt.Sprintf(" ORDER BY %s DESC, %s DESC", cursorField, idField)
	} else {
		query += fmt.Sprintf(" ORDER BY %s ASC, %s ASC", cursorField, idField)
	}

	// Add limit
	limit := p.GetLimit()
	query += fmt.Sprintf(" LIMIT %d", limit)

	return query, args, nil
}

// CreatePageInfo creates PageInfo from results
func CreatePageInfo(nodes interface{}, params *Params, totalCount *int) *PageInfo {
	pageInfo := &PageInfo{}

	if totalCount != nil {
		pageInfo.TotalCount = totalCount
	}

	// This is a simplified implementation
	// In a full implementation, you'd check if there are more results
	// based on the limit + 1 query pattern

	pageInfo.HasNextPage = false
	pageInfo.HasPreviousPage = params.After != nil || params.Before != nil

	return pageInfo
}

// ScanCursor scans a cursor from database row
func ScanCursor(rows interface{}, cursorField, idField string) ([]*Edge, error) {
	// This is a placeholder - actual implementation would depend on your database scanning logic
	// You'd typically scan database rows and create edges with cursors

	edges := []*Edge{}

	// Example implementation (adapt to your needs):
	// for each row {
	//     node := scanNode(row)
	//     cursor := &Cursor{
	//         ID: row[idField],
	//         SortValue: row[cursorField],
	//     }
	//     edge := &Edge{
	//         Node: node,
	//         Cursor: cursor.Encode(),
	//     }
	//     edges = append(edges, edge)
	// }

	return edges, nil
}

// LegacyPagination represents traditional offset-based pagination
type LegacyPagination struct {
	Page     int `json:"page" form:"page"`
	PageSize int `json:"pageSize" form:"page_size"`
}

// Validate validates legacy pagination parameters
func (p *LegacyPagination) Validate() error {
	if p.Page < 1 {
		p.Page = 1
	}

	if p.PageSize < 1 || p.PageSize > 100 {
		p.PageSize = 20 // Default page size
	}

	return nil
}

// GetOffset returns the offset for legacy pagination
func (p *LegacyPagination) GetOffset() int {
	return (p.Page - 1) * p.PageSize
}

// GetLimit returns the limit for legacy pagination
func (p *LegacyPagination) GetLimit() int {
	return p.PageSize
}

// LegacyPageInfo represents page information for legacy pagination
type LegacyPageInfo struct {
	CurrentPage  int  `json:"currentPage"`
	TotalPages   int  `json:"totalPages"`
	TotalRecords int  `json:"totalRecords"`
	HasNext      bool `json:"hasNext"`
	HasPrevious  bool `json:"hasPrevious"`
}

// CreateLegacyPageInfo creates legacy page info
func CreateLegacyPageInfo(currentPage, pageSize, totalRecords int) *LegacyPageInfo {
	totalPages := (totalRecords + pageSize - 1) / pageSize

	return &LegacyPageInfo{
		CurrentPage:  currentPage,
		TotalPages:   totalPages,
		TotalRecords: totalRecords,
		HasNext:      currentPage < totalPages,
		HasPrevious:  currentPage > 1,
	}
}
