package editor

import "strings"

// Dialect definitions.
//
// Keyword lists are deliberately not exhaustive against every engine's
// reserved-word table. Highlighting exists to make structure legible, and
// colouring every obscure reserved word adds noise without adding meaning. The
// lists cover what appears in real queries.

func words(s string) map[string]bool {
	out := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		out[w] = true
	}
	return out
}

const commonKeywords = `
select from where group by having order limit offset union all distinct
insert into values update set delete truncate merge using returning
create alter drop table view index unique constraint primary foreign key
references check default not null cascade restrict rename add column
join inner left right full outer cross on natural lateral
and or in exists between like ilike similar is as case when then else end
asc desc nulls first last with recursive over partition window
begin commit rollback savepoint transaction start
grant revoke to public role user password
if while loop for return declare exception raise
schema database sequence trigger procedure function returns language
temporary temp materialized concurrently cascade analyze explain vacuum
`

const commonTypes = `
int integer smallint bigint decimal numeric real float double precision
serial bigserial smallserial money
char varchar text nchar nvarchar ntext string
date time timestamp timestamptz datetime datetime2 interval year
boolean bool bit binary varbinary bytea blob clob
json jsonb xml uuid inet cidr macaddr array enum geometry geography
tinyint mediumint set tuple map list frozen counter varint timeuuid
`

const commonFunctions = `
count sum avg min max coalesce nullif greatest least cast convert
now current_date current_time current_timestamp localtime localtimestamp
extract date_trunc date_part age to_char to_date to_timestamp to_number
upper lower initcap trim ltrim rtrim lpad rpad substring substr replace
length char_length octet_length position strpos split_part concat concat_ws
abs ceil ceiling floor round trunc sign power sqrt exp ln log mod random
row_number rank dense_rank ntile lag lead first_value last_value nth_value
percentile_cont percentile_disc cume_dist percent_rank
array_agg string_agg json_agg jsonb_agg json_build_object jsonb_build_object
generate_series unnest exists any some regexp_replace regexp_matches
uuid_generate_v4 gen_random_uuid md5 encode decode
`

// PostgreSQL is the reference dialect.
var PostgreSQL = &Dialect{
	Name: "postgresql",
	Keywords: words(commonKeywords + `
		ilike offset returning conflict do nothing excluded
		unlogged inherits tablespace collate operator extension
		notify listen unlisten copy stdin stdout
	`),
	Types:               words(commonTypes + ` tsvector tsquery int4range int8range numrange tstzrange daterange oid regclass `),
	Functions:           words(commonFunctions + ` array_to_string to_jsonb row_to_json jsonb_set jsonb_path_query pg_typeof `),
	QuoteIdent:          '"',
	DollarQuote:         true,
	NestedBlockComments: true,
}

// MySQL also covers MariaDB, which shares the lexical surface.
var MySQL = &Dialect{
	Name: "mysql",
	Keywords: words(commonKeywords + `
		auto_increment engine charset collate unsigned zerofill
		straight_join ignore duplicate replace low_priority delayed
		show databases tables columns status variables describe
	`),
	Types:            words(commonTypes + ` tinytext mediumtext longtext tinyblob mediumblob longblob year `),
	Functions:        words(commonFunctions + ` ifnull group_concat date_format str_to_date unix_timestamp from_unixtime `),
	QuoteIdent:       '`',
	HashComment:      true,
	BackslashEscapes: true,
}

// SQLite.
var SQLite = &Dialect{
	Name:         "sqlite",
	Keywords:     words(commonKeywords + ` autoincrement without rowid pragma vacuum attach detach glob `),
	Types:        words(commonTypes),
	Functions:    words(commonFunctions + ` ifnull instr printf typeof last_insert_rowid json_extract `),
	QuoteIdent:   '"',
	BracketIdent: true,
}

// SQLServer covers T-SQL, which Chroma has no lexer for at all.
var SQLServer = &Dialect{
	Name: "sqlserver",
	Keywords: words(commonKeywords + `
		top nvarchar identity clustered nonclustered go
		try catch throw output cross apply outer pivot unpivot
		nolock rowlock readuncommitted isolation level
	`),
	Types:        words(commonTypes + ` uniqueidentifier smalldatetime datetimeoffset sql_variant hierarchyid rowversion `),
	Functions:    words(commonFunctions + ` isnull getdate datediff dateadd datename charindex stuff patindex newid `),
	QuoteIdent:   '"',
	BracketIdent: true,
}

// CQL is Cassandra's query language (FR-12.3, FR-5.1).
var CQL = &Dialect{
	Name: "cql",
	Keywords: words(commonKeywords + `
		keyspace columnfamily materialized allow filtering
		consistency ttl writetime token clustering compact storage
		replication durable_writes
	`),
	Types:      words(commonTypes + ` ascii bigint blob counter decimal duration inet smallint tinyint timeuuid varint frozen tuple `),
	Functions:  words(commonFunctions + ` now uuid mintimeuuid maxtimeuuid totimestamp todate tounixtimestamp `),
	QuoteIdent: '"',
}

// dialects maps a source's declared query language (capability.Query.Language)
// to a lexer dialect.
var dialects = map[string]*Dialect{
	"postgresql":  PostgreSQL,
	"postgres":    PostgreSQL,
	"cockroachdb": PostgreSQL,
	"redshift":    PostgreSQL,
	"mysql":       MySQL,
	"mariadb":     MySQL,
	"sqlite":      SQLite,
	"sqlserver":   SQLServer,
	"mssql":       SQLServer,
	"cql":         CQL,
	"cassandra":   CQL,
}

// DialectFor resolves a language name to a dialect, falling back to PostgreSQL.
//
// Falling back rather than failing is deliberate: an unknown dialect should
// produce approximate highlighting, never none. Being wrong about whether
// LATERAL is a keyword is a much smaller problem than a wall of grey text.
func DialectFor(language string) *Dialect {
	if d, ok := dialects[strings.ToLower(language)]; ok {
		return d
	}
	return PostgreSQL
}
