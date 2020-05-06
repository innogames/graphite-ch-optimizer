// Package main provides the watcher for the in time merged partitions
// Copyright (C) 2019 InnoGames GmbH
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go"
	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var version = "development"

// SelectUnmerged is the query to create the temporary table with
// partitions and the retention age, which should be applied.
// Table name should be with backquotes to be able to OPTIMIZE `database`.`.inner.table`
// for MaterializedView engines
const SelectUnmerged = `
SELECT
	concat(` + "'`', p.database, '`.`', p.table, '`'" + `) AS table,
	p.partition_id AS partition_id,
	p.partition AS partition_name,
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
	partition_name,
	partition_id
-- modified_at < rollup_time: the merge has not been applied for the current retention policy
-- parts > 1: merge should be applied because of new parts
-- modified_at < (now() - @Interval): we want to merge active partitions only once an interval
-- @Interval < age: do not touch currently active partitions
HAVING ((modified_at < rollup_time) OR (parts > 1))
	AND (modified_at < (now() - @Interval))
	AND ( @Interval < age)
ORDER BY
	table ASC,
	partition_name ASC,
	age ASC
`

type merge struct {
	table         string
	partitionID   string
	partitionName string
}

type clickHouse struct {
	ServerDsn        string        `mapstructure:"server-dsn" toml:"server-dsn"`
	OptimizeInterval time.Duration `mapstructure:"optimize-interval" toml:"optimize-interval"`
	connect          *sql.DB
}

type daemon struct {
	OneShot      bool          `mapstructure:"one-shot" toml:"one-shot"`
	LoopInterval time.Duration `mapstructure:"loop-interval" toml:"loop-interval"`
	DryRun       bool          `mapstructure:"dry-run" toml:"dry-run"`
}

type logging struct {
	// List of files to write. '-' is token as os.Stdout
	Output string `mapstructure:"output" toml:"output"`
	Level  string `mapstructure:"log-level" toml:"level"`
}

// Config for the graphite-ch-optimizer binary
type Config struct {
	ClickHouse clickHouse `mapstructure:"clickhouse" toml:"clickhouse"`
	Daemon     daemon     `mapstructure:"daemon" toml:"daemon"`
	Logging    logging    `mapstructure:"logging" toml:"logging"`
}

var cfg Config

func init() {
	var err error
	cfg = getConfig()

	// Set logging
	formatter := logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05 MST",
		FullTimestamp:   true,
	}
	logrus.SetFormatter(&formatter)
	var output io.Writer
	switch cfg.Logging.Output {
	case "-":
		output = os.Stdout
	default:
		output, err = os.OpenFile(cfg.Logging.Output, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			logrus.Fatalf("Unable to open file %s for writing: %s", cfg.Logging.Output, err)
		}
	}
	logrus.SetOutput(output)
	level, err := logrus.ParseLevel(cfg.Logging.Level)
	if err != nil {
		logrus.Fatal(fmt.Sprintf("Fail to parse log level: %v", err))
	}
	logrus.SetLevel(level)

	configString, _ := toml.Marshal(cfg)
	logrus.Tracef("The config is:\n%v", string(configString))
}

// setDefaultConfig sets default config parameters
func setDefaultConfig() {
	viper.SetDefault("clickhouse", map[string]interface{}{
		// See ClickHouse documentation for further options
		"server-dsn": "tcp://localhost:9000?&optimize_throw_if_noop=1&read_timeout=3600&debug=true",
		// Ignore partitions which were merged less than 3 days before
		"optimize-interval": time.Duration(72) * time.Hour,
	})
	viper.SetDefault("daemon", map[string]interface{}{
		"one-shot":      false,
		"loop-interval": time.Duration(1) * time.Hour,
		"dry-run":       false,
	})
	viper.SetDefault("logging", map[string]interface{}{
		"output":    "-",
		"log-level": "info",
	})
}

func processFlags() error {
	// Parse command line arguments in differend flag groups
	pflag.CommandLine.SortFlags = false
	customConfig := pflag.StringP("config", "c", "", "Filename of the custom config. CLI arguments override it")
	pflag.Bool("print-defaults", false, "Print default config values and exit")
	pflag.BoolP("version", "v", false, "Print version and exit")

	// ClickHouse set
	fc := pflag.NewFlagSet("clickhouse", 0)
	fc.StringP("server-dsn", "s", viper.GetString("clickhouse.server-dsn"), "DSN to connect to ClickHouse server")
	fc.Duration("optimize-interval", viper.GetDuration("clickhouse.optimize-interval"), "The active partitions won't be optimized more than once per this interval, seconds")
	// Daemon set
	fd := pflag.NewFlagSet("daemon", 0)
	fd.Bool("one-shot", viper.GetBool("daemon.one-shot"), "Program will make only one optimization instead of working in the loop (true if dry-run)")
	fd.Duration("loop-interval", viper.GetDuration("daemon.loop-interval"), "Daemon will check if there partitions to merge once per this interval, seconds")
	fd.BoolP("dry-run", "n", viper.GetBool("daemon.dry-run"), "Will print how many partitions would be merged without actions")
	// Logging set
	fl := pflag.NewFlagSet("logging", 0)
	fl.String("output", viper.GetString("logging.output"), "The logs file. '-' is accepted as STDOUT")
	fl.String("log-level", viper.GetString("logging.level"), "Valid options are: panic, fatal, error, warn, warning, info, debug, trace")

	pflag.CommandLine.AddFlagSet(fc)
	pflag.CommandLine.AddFlagSet(fd)
	pflag.CommandLine.AddFlagSet(fl)

	pflag.ErrHelp = fmt.Errorf("\nVersion: %s", version)
	pflag.Parse()
	// We must read config files before the setting of the config config to flags' values
	err := readConfigFile(*customConfig)
	if err != nil {
		return err
	}

	// Parse flag groups into viper config
	fc.VisitAll(func(f *pflag.Flag) {
		viper.BindPFlag("clickhouse."+f.Name, f)
	})
	fd.VisitAll(func(f *pflag.Flag) {
		viper.BindPFlag("daemon."+f.Name, f)
	})
	fl.VisitAll(func(f *pflag.Flag) {
		viper.BindPFlag("logging."+f.Name, f)
	})

	// If it's dry run, then it should be done only once
	if viper.GetBool("daemon.dry-run") {
		viper.Set("daemon.one-shot", true)
	}

	return nil
}

// readConfigFile set file as the config name if it's not empty and reads the config from Viper.configPaths
func readConfigFile(file string) error {
	var cfgNotFound viper.ConfigFileNotFoundError
	viper.SetConfigFile(file)
	err := viper.ReadInConfig()
	if err != nil {
		if errors.As(err, &cfgNotFound) {
			logrus.Debug("No config files were found, use defaults and flags")
			return nil
		}
		return fmt.Errorf("Failed to read viper config: %w", err)
	}
	return nil
}

func getConfig() Config {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	exeName := filepath.Base(os.Args[0])

	// Set config files
	if userConfig, err := os.UserConfigDir(); err == nil {
		viper.AddConfigPath(filepath.Join(userConfig, exeName))
	}
	viper.AddConfigPath(filepath.Join("/etc", exeName))

	setDefaultConfig()
	defaultConfig := viper.AllSettings()

	err := processFlags()
	if err != nil {
		logrus.Fatalf("Failed to process flags: %v", err)
	}

	// Prints version and exit
	printVersion, err := pflag.CommandLine.GetBool("version")
	if err != nil {
		logrus.Fatal("Can't get '--version' value")
	}
	if printVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Prints default config and exits
	printDefaults, err := pflag.CommandLine.GetBool("print-defaults")
	if err != nil {
		logrus.Fatal("Can't get '--print-defaults' value")
	}
	if printDefaults {
		t, err := toml.TreeFromMap(defaultConfig)
		if err != nil {
			logrus.Fatal(err)
		}
		fmt.Println(t.String())
		os.Exit(0)
	}

	c := Config{}
	viper.Unmarshal(&c)
	return c
}

func main() {
	if cfg.Daemon.OneShot {
		optimize()
		os.Exit(0)
	}

	go func() {
		logrus.Trace("Starting loop function")
		for {
			err := optimize()
			if err != nil {
				logrus.Errorf("Optimization failed: %s", err)
			}
			logrus.Infof("Optimizations round is over, going to sleep for %v", cfg.Daemon.LoopInterval)
			time.Sleep(cfg.Daemon.LoopInterval)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}

func optimize() error {
	// Getting connection pool and check it for work
	cfg.ClickHouse.connect, _ = sql.Open("clickhouse", cfg.ClickHouse.ServerDsn)
	connect := cfg.ClickHouse.connect
	defer connect.Close()
	err := connect.Ping()
	if checkErr(err) != nil {
		logrus.Fatalf("Ping ClickHouse server failed: %v", err)
	}

	// Getting the rows with tables and partitions to optimize
	rows, err := connect.Query(
		SelectUnmerged,
		sql.Named("Interval", cfg.ClickHouse.OptimizeInterval.Seconds()),
	)
	if checkErr(err) != nil {
		return err
	}

	merges := []merge{}
	var (
		age        uint64
		parts      uint64
		maxTime    time.Time
		rollupTime time.Time
		modifiedAt time.Time
	)

	// Parse the data from DB into `merges`
	for rows.Next() {
		var m merge
		err = rows.Scan(&m.table, &m.partitionID, &m.partitionName, &age, &parts, &maxTime, &rollupTime, &modifiedAt)
		if checkErr(err) != nil {
			return err
		}
		merges = append(merges, m)
		logrus.WithFields(logrus.Fields{
			"table":          m.table,
			"partition_id":   m.partitionID,
			"partition_name": m.partitionName,
			"age":            age,
			"parts":          parts,
			"max_time":       maxTime,
			"rollup_time":    rollupTime,
			"modified_at":    modifiedAt,
		}).Debug("Merge to be applied")
	}

	if cfg.Daemon.DryRun {
		logrus.Infof("DRY RUN. Merges would be applied: %d", len(merges))
		return nil
	}
	logrus.Infof("Merges will be applied: %d", len(merges))

	for _, m := range merges {
		err = applyMerge(&m)
		if checkErr(err) != nil {
			return err
		}
	}
	return nil
}

func applyMerge(m *merge) error {
	logrus.Infof("Going to merge TABLE %s PARTITION %s", m.table, m.partitionName)
	_, err := cfg.ClickHouse.connect.Exec(
		fmt.Sprintf(
			"OPTIMIZE TABLE %s PARTITION ID '%s' FINAL",
			m.table,
			m.partitionID,
		),
	)
	if err != nil {
		var chExc *clickhouse.Exception
		if errors.As(err, &chExc) {
			if chExc.Code == 388 && strings.Contains(chExc.Message, "has already been assigned a merge into") {
				logrus.WithFields(logrus.Fields{
					"table":          m.table,
					"partition_name": m.partitionName,
				}).Info("The partition is already merging:")
				return nil
			}
		}
		return fmt.Errorf("Fail to merge partition %v: %w", m.partitionName, checkErr(err))
	}
	return nil
}

func checkErr(err error) error {
	var chExc *clickhouse.Exception
	if err == nil {
		return err
	}
	if errors.As(err, &chExc) {
		logrus.Errorf(
			"[%d] %s \n%s\n",
			chExc.Code,
			chExc.Message,
			chExc.StackTrace,
		)
	} else {
		logrus.Errorf("Fail: %v", err)
	}
	return err
}
