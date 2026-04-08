package repository

import sq "github.com/Masterminds/squirrel"

// Dialect represents a SQL database dialect.
type Dialect int

const (
	DialectSQLite   Dialect = iota
	DialectPostgres
)

// Builder returns a squirrel StatementBuilder configured for this dialect.
func (d Dialect) Builder() sq.StatementBuilderType {
	if d == DialectPostgres {
		return sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	}
	return sq.StatementBuilder.PlaceholderFormat(sq.Question)
}
