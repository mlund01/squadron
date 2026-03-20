package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "github.com/databricks/databricks-sql-go"
	"github.com/mlund01/squadron-sdk"
)

var tools = map[string]*squadron.ToolInfo{
	"execute_sql": {
		Name:        "execute_sql",
		Description: "Execute a SQL query against Databricks and return results as JSON. Use for SELECT queries, DDL, or DML statements.",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"query": {
					Type:        squadron.TypeString,
					Description: "The SQL query to execute",
				},
				"catalog": {
					Type:        squadron.TypeString,
					Description: "The catalog to query in (optional, uses connection default if not specified)",
				},
				"schema": {
					Type:        squadron.TypeString,
					Description: "The schema to query in (optional, uses connection default if not specified)",
				},
				"max_rows": {
					Type:        squadron.TypeInteger,
					Description: "Maximum number of rows to return (default: 1000, max: 10000)",
				},
			},
			Required: []string{"query"},
		},
	},
	"list_schemas": {
		Name:        "list_schemas",
		Description: "List all schemas in a catalog",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"catalog": {
					Type:        squadron.TypeString,
					Description: "The catalog to list schemas from (optional)",
				},
			},
		},
	},
	"list_tables": {
		Name:        "list_tables",
		Description: "List all tables in a schema",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"catalog": {
					Type:        squadron.TypeString,
					Description: "The catalog containing the schema (optional)",
				},
				"schema": {
					Type:        squadron.TypeString,
					Description: "The schema name to list tables from (required)",
				},
			},
			Required: []string{"schema"},
		},
	},
	"describe_table": {
		Name:        "describe_table",
		Description: "Get the column names, types, and comments for a table",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"table": {
					Type:        squadron.TypeString,
					Description: "The table name (can be catalog.schema.table, schema.table, or just table)",
				},
			},
			Required: []string{"table"},
		},
	},
}

type DatabricksPlugin struct {
	mu sync.Mutex
	db *sql.DB
}

func (p *DatabricksPlugin) Configure(settings map[string]string) error {
	host := settings["host"]
	token := settings["token"]
	httpPath := settings["http_path"]

	if host == "" {
		return fmt.Errorf("'host' setting is required")
	}
	if token == "" {
		return fmt.Errorf("'token' setting is required")
	}
	if httpPath == "" {
		return fmt.Errorf("'http_path' setting is required")
	}

	dsn := fmt.Sprintf("token:%s@%s:443%s", token, host, httpPath)

	db, err := sql.Open("databricks", dsn)
	if err != nil {
		return fmt.Errorf("failed to open databricks connection: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("failed to connect to databricks: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.db = db

	return nil
}

func (p *DatabricksPlugin) Call(toolName string, payload string) (string, error) {
	p.mu.Lock()
	db := p.db
	p.mu.Unlock()

	if db == nil {
		return "", fmt.Errorf("plugin not configured — call Configure first")
	}

	switch toolName {
	case "execute_sql":
		return p.executeSQL(db, payload)
	case "list_schemas":
		return p.listSchemas(db, payload)
	case "list_tables":
		return p.listTables(db, payload)
	case "describe_table":
		return p.describeTable(db, payload)
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (p *DatabricksPlugin) GetToolInfo(toolName string) (*squadron.ToolInfo, error) {
	info, ok := tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return info, nil
}

func (p *DatabricksPlugin) ListTools() ([]*squadron.ToolInfo, error) {
	result := make([]*squadron.ToolInfo, 0, len(tools))
	for _, info := range tools {
		result = append(result, info)
	}
	return result, nil
}

// useCatalogSchema runs USE CATALOG and USE SCHEMA if provided, scoped to a single call
func (p *DatabricksPlugin) useCatalogSchema(db *sql.DB, catalog, schema string) error {
	if catalog != "" {
		if _, err := db.Exec("USE CATALOG " + catalog); err != nil {
			return fmt.Errorf("failed to set catalog: %w", err)
		}
	}
	if schema != "" {
		if _, err := db.Exec("USE SCHEMA " + schema); err != nil {
			return fmt.Errorf("failed to set schema: %w", err)
		}
	}
	return nil
}

// executeSQL runs a SQL query and returns results as JSON
func (p *DatabricksPlugin) executeSQL(db *sql.DB, payload string) (string, error) {
	var params struct {
		Query   string `json:"query"`
		Catalog string `json:"catalog"`
		Schema  string `json:"schema"`
		MaxRows int    `json:"max_rows"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}
	if params.Query == "" {
		return "", fmt.Errorf("'query' is required")
	}
	if params.MaxRows <= 0 {
		params.MaxRows = 1000
	}
	if params.MaxRows > 10000 {
		params.MaxRows = 10000
	}

	if err := p.useCatalogSchema(db, params.Catalog, params.Schema); err != nil {
		return "", err
	}

	// Detect if this is a non-SELECT statement
	trimmed := strings.TrimSpace(strings.ToUpper(params.Query))
	isSelect := strings.HasPrefix(trimmed, "SELECT") || strings.HasPrefix(trimmed, "SHOW") || strings.HasPrefix(trimmed, "DESCRIBE") || strings.HasPrefix(trimmed, "EXPLAIN") || strings.HasPrefix(trimmed, "WITH")

	if !isSelect {
		result, err := db.Exec(params.Query)
		if err != nil {
			return "", fmt.Errorf("query execution failed: %w", err)
		}
		rowsAffected, _ := result.RowsAffected()
		return fmt.Sprintf(`{"status": "ok", "rows_affected": %d}`, rowsAffected), nil
	}

	rows, err := db.Query(params.Query)
	if err != nil {
		return "", fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("failed to get columns: %w", err)
	}

	var results []map[string]any
	count := 0

	for rows.Next() && count < params.MaxRows {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return "", fmt.Errorf("failed to scan row: %w", err)
		}

		row := make(map[string]any)
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		results = append(results, row)
		count++
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("row iteration error: %w", err)
	}

	output := map[string]any{
		"columns":   columns,
		"rows":      results,
		"row_count": len(results),
	}
	if len(results) == params.MaxRows {
		output["truncated"] = true
	}

	b, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}
	return string(b), nil
}

// listSchemas returns all schemas in a catalog
func (p *DatabricksPlugin) listSchemas(db *sql.DB, payload string) (string, error) {
	var params struct {
		Catalog string `json:"catalog"`
	}
	if payload != "" && payload != "{}" {
		json.Unmarshal([]byte(payload), &params)
	}

	query := "SHOW SCHEMAS"
	if params.Catalog != "" {
		query += " IN " + params.Catalog
	}

	rows, err := db.Query(query)
	if err != nil {
		return "", fmt.Errorf("failed to list schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", fmt.Errorf("failed to scan schema: %w", err)
		}
		schemas = append(schemas, name)
	}

	b, err := json.Marshal(map[string]any{"schemas": schemas})
	if err != nil {
		return "", fmt.Errorf("failed to marshal schemas: %w", err)
	}
	return string(b), nil
}

// listTables returns all tables in a schema
func (p *DatabricksPlugin) listTables(db *sql.DB, payload string) (string, error) {
	var params struct {
		Catalog string `json:"catalog"`
		Schema  string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	if params.Schema == "" {
		return "", fmt.Errorf("'schema' is required")
	}

	qualifier := params.Schema
	if params.Catalog != "" {
		qualifier = params.Catalog + "." + params.Schema
	}

	rows, err := db.Query("SHOW TABLES IN " + qualifier)
	if err != nil {
		return "", fmt.Errorf("failed to list tables: %w", err)
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var tables []map[string]any

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return "", fmt.Errorf("failed to scan table: %w", err)
		}
		row := make(map[string]any)
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		tables = append(tables, row)
	}

	b, err := json.Marshal(map[string]any{"tables": tables})
	if err != nil {
		return "", fmt.Errorf("failed to marshal tables: %w", err)
	}
	return string(b), nil
}

// describeTable returns column info for a table
func (p *DatabricksPlugin) describeTable(db *sql.DB, payload string) (string, error) {
	var params struct {
		Table string `json:"table"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}
	if params.Table == "" {
		return "", fmt.Errorf("'table' is required")
	}

	rows, err := db.Query("DESCRIBE TABLE " + params.Table)
	if err != nil {
		return "", fmt.Errorf("failed to describe table: %w", err)
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var columnInfos []map[string]any

	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return "", fmt.Errorf("failed to scan column info: %w", err)
		}
		row := make(map[string]any)
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		columnInfos = append(columnInfos, row)
	}

	b, err := json.Marshal(map[string]any{"table": params.Table, "columns": columnInfos})
	if err != nil {
		return "", fmt.Errorf("failed to marshal column info: %w", err)
	}
	return string(b), nil
}

func main() {
	squadron.Serve(&DatabricksPlugin{})
}
