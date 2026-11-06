// Package databaseconfig owns the Article example's environment names and
// explicit CLI override policy. Generic configuration and resource opening
// belong to settings/database.
package databaseconfig

import "github.com/progresshans/godj/settings/database"

const (
	SQLiteDatabaseEnv = "GODJ_ARTICLE_SQLITE_DATABASE"
	PostgresURLEnv    = "GODJ_ARTICLE_POSTGRES_URL"
	PostgresSchemaEnv = "GODJ_ARTICLE_POSTGRES_SCHEMA"
)

func environmentNames() database.EnvironmentNames {
	return database.EnvironmentNames{SQLiteDatabase: SQLiteDatabaseEnv, PostgresURL: PostgresURLEnv, PostgresSchema: PostgresSchemaEnv}
}

// FromEnvironment requires an explicit Article database; the example does not
// inherit the new-project scaffold's default SQLite selection.
func FromEnvironment(lookup database.LookupEnvFunc) (database.Config, error) {
	return database.FromEnvironment(lookup, database.Options{Names: environmentNames()})
}

// ForServe rejects an explicit SQLite flag combined with any database key.
func ForServe(directSQLite string, directSpecified bool, lookup database.LookupEnvFunc) (database.Config, error) {
	var override database.Config
	if directSpecified {
		var err error
		override, err = database.SQLite(directSQLite)
		if err != nil {
			return database.Config{}, err
		}
	}
	return database.FromEnvironment(lookup, database.Options{Names: environmentNames(), Override: override})
}
