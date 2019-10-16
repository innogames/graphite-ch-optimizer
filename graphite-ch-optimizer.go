// Package main provides the watcher for the in time merged partitions
// Copyright (C) 2019 InnoGames GmbH
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kshvakov/clickhouse"
	toml "github.com/pelletier/go-toml"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// SelectUnmerged is the query to create the temporary table with
// partitions and the retention age, which should be applied
const SelectUnmerged = `
SELECT
	concat(p.database, '.', p.table) AS table,
	p.partition AS partition,
	max(g.age) AS age,
	countDistinct(p.name) AS parts,
	toDateTime(max(p.max_date + 1)) AS max_time,
	max_time + age AS rollup_time,
	min(p.modification_time) AS modified_at
FROM system.parts AS p
INNER JOIN
(
	SELECT
		Tables.database AS database,
		Tables.table AS table,
		age
	FROM system.graphite_retentions
	ARRAY JOIN Tables
	GROUP BY
		database,
		table,
		age
) AS g ON (p.table = g.table) AND (p.database = g.database)
-- toDateTime(p.max_date + 1) + g.age AS unaggregated rollup_time
WHERE p.active AND ((toDateTime(p.max_date + 1) + g.age) < now())
GROUP BY
	table,
	partition
-- modified_at < rollup_time: the merge has not been applied for the current retention policy
-- parts > 1: merge should be applied because of new parts
-- modified_at < (now() - @Interval): we want to merge active partitions only once an interval
-- @Interval < age: do not touch currently active partitions
HAVING ((modified_at < rollup_time) OR (parts > 1))
	AND (modified_at < (now() - @Interval))
	AND ( @Interval < age)
ORDER BY
	table ASC,
	partition ASC,
	age ASC
`

type merge struct {
	table     string
	partition string
}

type clickHouse struct {
	ServerDsn        string `mapstructure:"server-dsn"`
	OptimizeInterval uint   `mapstructure:"optimize-interval"`
}

type daemon struct {
	OneShot      bool `mapstructure:"one-shot"`
	LoopInterval uint `mapstructure:"loop-interval"`
	DryRun       bool `mapstructure:"dry-run"`
}

type logging struct {
	// List of files to write. '-' is token as os.Stdout
	Output string `mapstructure:"output"`
	Level  string `mapstructure:"log-level"`
}

// Config for the graphite-ch-optimizer binary
type Config struct {
	ClickHouse clickHouse `mapstructure:"clickhouse"`
	Daemon     daemon     `mapstructure:"daemon"`
	Logging    logging    `mapstructure:"logging"`
}

var cfg = &Config{}

func init() {
	var err error
	cfg, err = getConfig()
	checkErr(err)
	formatter := log.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05 MST",
		FullTimestamp:   true,
	}
	log.SetFormatter(&formatter)
	var output io.Writer
	switch cfg.Logging.Output {
	case "-":
		output = os.Stdout
	default:
		output, err = os.OpenFile(cfg.Logging.Output, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("Unable to open file %s for writing: %s", cfg.Logging.Output, err)
		}
	}
	log.SetOutput(output)
	level, err := log.ParseLevel(cfg.Logging.Level)
	if err != nil {
		log.Fatal(fmt.Sprintf("Fail to parse log level: %v", err))
	}
	log.SetLevel(level)
}

func getConfig() (*Config, error) {
	c := &Config{}
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("toml")
	if userConfig, err := os.UserConfigDir(); err == nil {
		v.AddConfigPath(userConfig)
	}
	v.AddConfigPath("/etc/graphite-ch-optimizer")

	v.SetDefault("clickhouse", map[string]interface{}{
		// See ClickHouse documentation for further options
		"server-dsn": "tcp://localhost:9000?&optimize_throw_if_noop=1&read_timeout=3600&debug=true",
		// Ignore partitions which were merged less than 3 days before
		"optimize-interval": 60 * 60 * 24 * 3,
	})
	v.SetDefault("daemon", map[string]interface{}{
		"one-shot":      false,
		"loop-interval": 60 * 60,
		"dry-run":       false,
	})
	v.SetDefault("logging", map[string]interface{}{
		"output":    "-",
		"log-level": "info",
	})
	defaultConfig := v.AllSettings()
	log.Traceln(defaultConfig)
	pflag.CommandLine.SortFlags = false
	customConfig := pflag.StringP("config", "c", "", "Filename of the custom config. CLI arguments override it")
	fc := pflag.NewFlagSet("clickhouse", 0)
	fd := pflag.NewFlagSet("daemon", 0)
	fl := pflag.NewFlagSet("logging", 0)
	printDefaults := pflag.Bool("print-defaults", false, "Print default config values and exit")
	fc.StringP("server-dsn", "s", v.GetString("clickhouse.server-dsn"), "DSN to connect to ClickHouse server")
	fc.Uint("optimize-interval", v.GetUint("clickhouse.optimize-interval"), "The active partitions won't be optimized more than once per this interval, seconds")
	fd.Bool("one-shot", v.GetBool("daemon.one-shot"), "Program will make only one optimization instead of working in the loop (true if dry-run)")
	fd.Uint("loop-interval", v.GetUint("daemon.loop-interval"), "Daemon will check if there partitions to merge once per this interval, seconds")
	fd.BoolP("dry-run", "n", v.GetBool("daemon.dry-run"), "Will print how many partitions would be merged without actions")
	fl.String("output", v.GetString("logging.output"), "The logs file. '-' is accepted as STDOUT")
	fl.String("log-level", v.GetString("logging.level"), "Valid options are: panic, fatal, error, warn, warning, info, debug, trace")
	pflag.CommandLine.AddFlagSet(fc)
	pflag.CommandLine.AddFlagSet(fd)
	pflag.CommandLine.AddFlagSet(fl)
	pflag.Parse()
	v.SetConfigFile(*customConfig)
	v.ReadInConfig()
	log.Trace(v.ConfigFileUsed())
	if *printDefaults {
		t, err := toml.TreeFromMap(v.AllSettings())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(t.String())
		os.Exit(0)
	}
	fc.VisitAll(func(f *pflag.Flag) {
		v.BindPFlag("clickhouse."+f.Name, f)
	})
	fd.VisitAll(func(f *pflag.Flag) {
		v.BindPFlag("daemon."+f.Name, f)
	})
	fl.VisitAll(func(f *pflag.Flag) {
		v.BindPFlag("logging."+f.Name, f)
	})
	if v.GetBool("daemon.dry-run") {
		v.Set("daemon.one-shot", true)
	}
	v.Unmarshal(c)
	log.Traceln(c)
	return c, nil
}

func main() {
	if cfg.Daemon.OneShot {
		optimize(cfg)
	} else {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			log.Trace("Starting loop function")
			for {
				err := optimize(cfg)
				if err != nil {
					log.Errorf("Optimization failed: %s", err)
				}
				time.Sleep(time.Second * time.Duration(cfg.Daemon.LoopInterval))
			}
		}()
		log.Trace("Starting loop function")
		wg.Wait()
	}
}

func optimize(cfg *Config) error {
	var chExc *clickhouse.Exception
	connect, err := sql.Open("clickhouse", cfg.ClickHouse.ServerDsn)
	if err := connect.Ping(); err != nil {
		if errors.As(err, &chExc) {
			log.Errorf(
				"[%d] %s \n%s\n",
				chExc.Code,
				chExc.Message,
				chExc.StackTrace,
			)
		} else {
			log.Errorln(err)
		}
		return err
	}
	rows, err := connect.Query(
		SelectUnmerged,
		sql.Named("Interval", cfg.ClickHouse.OptimizeInterval),
	)
	checkErr(err)
	columns, err := rows.Columns()
	checkErr(err)
	log.WithField("columns", columns).Debug("The columns in the query:")
	merges := []merge{}
	for rows.Next() {
		var (
			m          merge
			age        uint64
			parts      uint64
			maxTime    time.Time
			rollupTime time.Time
			modifiedAt time.Time
		)
		err = rows.Scan(&m.table, &m.partition, &age, &parts, &maxTime, &rollupTime, &modifiedAt)
		checkErr(err)
		merges = append(merges, m)
		log.WithFields(log.Fields{
			"table":       m.table,
			"partition":   m.partition,
			"age":         age,
			"parts":       parts,
			"max_time":    maxTime,
			"rollup_time": rollupTime,
			"modified_at": modifiedAt,
		}).Debug("Merge to be applied")
	}
	log.Infof("Merges will be applied: %d", len(merges))
	if cfg.Daemon.DryRun {
		log.Info("No merges are applied in dry-run mode")
		return nil
	}
	for _, m := range merges {
		log.Infof("Going to merge TABLE %s PARTITION %s", m.table, m.partition)
		_, err = connect.Exec(
			fmt.Sprintf(
				"OPTIMIZE TABLE %s PARTITION %s FINAL",
				m.table,
				m.partition,
			),
		)
		if err != nil {
			if errors.As(err, &chExc) {
				if chExc.Code == 388 && strings.Contains(chExc.Message, "has already been assigned a merge into") {
					log.WithFields(log.Fields{
						"table":     m.table,
						"partition": m.partition,
					}).Info("The partition is already merging:")
				} else {
					log.Errorf(
						"[%d] %s \n%s\n",
						chExc.Code,
						chExc.Message,
						chExc.StackTrace,
					)
				}
			} else {
				log.Fatal(err)
			}
		}
	}
	return nil
}

func checkErr(err error) {
	var chExc *clickhouse.Exception
	if err != nil {
		if errors.As(err, &chExc) {
			log.Errorf(
				"[%d] %s \n%s\n",
				chExc.Code,
				chExc.Message,
				chExc.StackTrace,
			)
		} else {
			log.Fatalln(fmt.Errorf("Fail %w", err))
		}
	}
}
