package db

import (
	"context"
	"database/sql"
	//_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
	_ "github.com/tursodatabase/go-libsql"
	"log"
	"log/slog"
	"os"
)

var Database *database

type database struct {
	DB       *sql.DB
	sqlUrl   string
	redisUrl string
	redis    *redis.Client
	redisCtx context.Context
}

func New() *database {
	dbName := os.Getenv("DB_URL")
	driverName := os.Getenv("DRIVER")
	redisURL := os.Getenv("REDIS_URL")
	// The connection string is not logged: it carries credentials on any
	// driver other than SQLite. Only the driver name is safe to print.
	slog.Info("connecting to database", "driver", driverName)
	db, dbconErr := sql.Open(driverName, dbName)
	if dbconErr != nil {
		slog.Error("could not open database", "driver", driverName, "error", dbconErr.Error())
		panic("could not open database: " + dbconErr.Error())
	}
	pingErr := db.Ping()
	//db.SetMaxOpenConns(1)
	if pingErr != nil {
		log.Fatal(pingErr)
	}
	rdb := redis.NewClient(&redis.Options{ // redis for later
		Addr:     redisURL,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	redisCtx := context.Background()
	rdsStatus := rdb.Ping(redisCtx)
	if rdsStatus.Err() != nil {
		slog.Error(rdsStatus.Err().Error())
		panic("Unable to connect to redis")
	}
	//TODO change this context a bit
	newDb := new(database)
	newDb.DB = db
	newDb.sqlUrl = dbName
	newDb.redisUrl = redisURL
	newDb.redisCtx = redisCtx
	newDb.redis = rdb
	slog.Info("Connected to database")
	return newDb
}

// todo figure out how to not lock the database
func UseSQL() *sql.DB {
	//db, dbconErr := sql.Open("libsql", Database.sqlUrl)
	//if dbconErr != nil {
	//	_ = fmt.Errorf(dbconErr.Error())
	//	panic(dbconErr)
	//}
	//Database.DB = db
	return Database.DB
}

func UseRedis() (*redis.Client, context.Context) {
	return Database.redis, Database.redisCtx
}
func (d *database) CloseSQL() {
	closeErr := d.DB.Close()
	if closeErr != nil {
		panic(closeErr)
	}
}
