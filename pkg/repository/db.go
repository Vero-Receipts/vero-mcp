package repository

import (
	"database/sql"

	sq "github.com/Masterminds/squirrel"
)

// BaseRepo provides shared database access and a squirrel builder for all repo implementations.
type BaseRepo struct {
	DB *sql.DB
	SQ sq.StatementBuilderType
}

// NewBaseRepo creates a BaseRepo with the correct placeholder format for the dialect.
func NewBaseRepo(db *sql.DB, dialect Dialect) BaseRepo {
	return BaseRepo{
		DB: db,
		SQ: dialect.Builder().RunWith(db),
	}
}
