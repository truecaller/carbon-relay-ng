// carbon-relay-ng
// route traffic to anything that speaks the Graphite Carbon protocol (text or pickle)
// such as Graphite's carbon-cache.py, influxdb, ...
package main

import (
	"bufio"
	"bytes"
	"expvar"
	"flag"
	"fmt"
	"io"
	"net"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/pprof"

	"github.com/graphite-ng/carbon-relay-ng/_third_party/github.com/BurntSushi/toml"
	"github.com/graphite-ng/carbon-relay-ng/_third_party/github.com/Dieterbe/go-metrics"
	"github.com/graphite-ng/carbon-relay-ng/_third_party/github.com/Dieterbe/go-metrics/exp"
	m20 "github.com/graphite-ng/carbon-relay-ng/_third_party/github.com/metrics20/go-metrics20"
	logging "github.com/graphite-ng/carbon-relay-ng/_third_party/github.com/op/go-logging"
	"github.com/graphite-ng/carbon-relay-ng/_third_party/github.com/rcrowley/goagain"
	"github.com/graphite-ng/carbon-relay-ng/badmetrics"
	//"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type MetricValidationLevel struct {
	Level m20.LegacyMetricValidation
}

func (l *MetricValidationLevel) UnmarshalText(text []byte) error {
	validationLevels := map[string]m20.LegacyMetricValidation{
		"strict": m20.Strict,
		"medium": m20.Medium,
		"none":   m20.None,
	}
	var err error
	var ok bool
	l.Level, ok = validationLevels[string(text)]
	if !ok {
		err = fmt.Errorf("Invalid validation level '%s'. Valid validation levels are 'strict', 'medium', and 'none'.", string(text))
	}
	return err
}

type Config struct {
	Listen_addr              string
	Admin_addr               string
	Http_addr                string
	Spool_dir                string
	max_procs                int
	First_only               bool
	Routes                   []*Route
	Init                     []string
	Instance                 string
	Log_level                string
	Instrumentation          instrumentation
	Bad_metrics_max_age      string
	Pid_file                 string
	Legacy_metric_validation MetricValidationLevel
}

type instrumentation struct {
	Graphite_addr     string
	Graphite_interval int
}

var (
	instance    string
	service     = "carbon-relay-ng"
	config_file string
	config      Config
	to_dispatch = make(chan []byte)
	table       *Table
	cpuprofile  = flag.String("cpuprofile", "", "write cpu profile to file")
	numIn       metrics.Counter
	numInvalid  metrics.Counter
	badMetrics  *badmetrics.BadMetrics
)

var log = logging.MustGetLogger("carbon-relay-ng")

func init() {
	var format = "%{color}%{time:15:04:05.000000} ▶ %{level:.4s} %{color:reset} %{message}"
	logBackend := logging.NewLogBackend(os.Stderr, "", 0)
	logging.SetFormatter(logging.MustStringFormatter(format))
	logging.SetBackend(logBackend)

	exp.Exp(metrics.DefaultRegistry)

}

func accept(l *net.TCPListener, config Config) {
	for {
		c, err := l.AcceptTCP()
		if nil != err {
			log.Error(err.Error())
			break
		}
		go handle(c, config)
	}
}

var emptyByteStr = []byte("")

func handle(c net.Conn, config Config) {
	defer c.Close()
	// TODO c.SetTimeout(60e9)
	r := bufio.NewReaderSize(c, 4096)
	var acc bytes.Buffer
	for {

		// Note that everything in this loop should proceed as fast as it can
		// so we're not blocked and can keep processing
		// so the validation, the pipeline initiated via table.Dispatch(), etc
		// must never block.

		// note that we don't support lines longer than 4096B. that seems very reasonable..
		part, isprefix, err := r.ReadLine()
		acc.Write(part)
		if isprefix {
			continue
		}
		buf := acc.Bytes()
		acc.Reset()

		if nil != err {
			if io.EOF != err {
				log.Error(err.Error())
			}
			break
		}

		numIn.Inc(1)

		err = m20.ValidatePacket(buf, config.Legacy_metric_validation.Level)
		if err != nil {
			fields := bytes.Fields(buf)
			if len(fields) != 0 {
				badMetrics.Add(fields[0], buf, err)
			} else {
				badMetrics.Add(emptyByteStr, buf, err)
			}
			numInvalid.Inc(1)
			continue
		}

		table.Dispatch(buf)
	}
}

func usage() {
	fmt.Fprintln(
		os.Stderr,
		"Usage: carbon-relay-ng <path-to-config>",
	)
	flag.PrintDefaults()
}

func main() {

	flag.Usage = usage
	flag.Parse()

	// Default to strict validation
	config.Legacy_metric_validation.Level = m20.Strict

	config_file = "/etc/carbon-relay-ng.ini"
	if 1 == flag.NArg() {
		config_file = flag.Arg(0)
	}

	if _, err := toml.DecodeFile(config_file, &config); err != nil {
		log.Error("Cannot use config file '%s':\n", config_file)
		log.Error(err.Error())
		usage()
		return
	}
	//runtime.SetBlockProfileRate(1) // to enable block profiling. in my experience, adds 35% overhead.

	levels := map[string]logging.Level{
		"critical": logging.CRITICAL,
		"error":    logging.ERROR,
		"warning":  logging.WARNING,
		"notice":   logging.NOTICE,
		"info":     logging.INFO,
		"debug":    logging.DEBUG,
	}
	level, ok := levels[config.Log_level]
	if !ok {
		log.Error("unrecognized log level '%s'\n", config.Log_level)
		return
	}
	logging.SetLevel(level, "carbon-relay-ng")
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if len(config.Instance) == 0 {
		log.Error("instance identifier cannot be empty")
		os.Exit(1)
	}

	runtime.GOMAXPROCS(config.max_procs)

	instance = config.Instance
	expvar.NewString("instance").Set(instance)
	expvar.NewString("service").Set(service)

	log.Notice("===== carbon-relay-ng instance '%s' starting. =====\n", instance)

	numIn = Counter("unit=Metric.direction=in")
	numInvalid = Counter("unit=Err.type=invalid")
	if config.Instrumentation.Graphite_addr != "" {
		addr, err := net.ResolveTCPAddr("tcp", config.Instrumentation.Graphite_addr)
		if err != nil {
			log.Fatal(err)
		}
		go metrics.Graphite(metrics.DefaultRegistry, time.Duration(config.Instrumentation.Graphite_interval)*time.Millisecond, "", addr)
	}

	log.Notice("creating routing table...")
	maxAge, err := time.ParseDuration(config.Bad_metrics_max_age)
	if err != nil {
		log.Error("could not parse badMetrics max age")
		log.Error(err.Error())
		os.Exit(1)
	}
	badMetrics = badmetrics.New(maxAge)
	table = NewTable(config.Spool_dir)
	log.Notice("initializing routing table...")
	for i, cmd := range config.Init {
		log.Notice("applying: %s", cmd)
		err = applyCommand(table, cmd)
		if err != nil {
			log.Error("could not apply init cmd #%d", i+1)
			log.Error(err.Error())
			os.Exit(1)
		}
	}
	tablePrinted := table.Print()
	log.Notice("===========================")
	log.Notice("========== TABLE ==========")
	log.Notice("===========================")
	for _, line := range strings.Split(tablePrinted, "\n") {
		log.Notice(line)
	}

	// Follow the goagain protocol, <https://github.com/rcrowley/goagain>.
	l, ppid, err := goagain.GetEnvs()
	if nil != err {
		laddr, err := net.ResolveTCPAddr("tcp", config.Listen_addr)
		if nil != err {
			log.Error(err.Error())
			os.Exit(1)
		}
		l, err = net.ListenTCP("tcp", laddr)
		if nil != err {
			log.Error(err.Error())

			os.Exit(1)
		}
		log.Notice("listening on %v/tcp", laddr)
		go accept(l.(*net.TCPListener), config)
	} else {
		log.Notice("resuming listening on %v/tcp", l.Addr())
		go accept(l.(*net.TCPListener), config)
		if err := goagain.KillParent(ppid); nil != err {
			log.Error(err.Error())
			os.Exit(1)
		}
		for {
			err := syscall.Kill(ppid, 0)
			if err != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	udp_addr, err := net.ResolveUDPAddr("udp", config.Listen_addr)
	if nil != err {
		log.Error(err.Error())
		os.Exit(1)
	}
	udp_conn, err := net.ListenUDP("udp", udp_addr)
	if nil != err {
		log.Error(err.Error())
		os.Exit(1)
	}
	log.Notice("listening on %v/udp", udp_addr)
	go handle(udp_conn, config)

	if config.Pid_file != "" {
		f, err := os.Create(config.Pid_file)
		if err != nil {
			fmt.Println("error creating pidfile:", err.Error())
			os.Exit(1)
		}
		_, err = f.Write([]byte(strconv.Itoa(os.Getpid())))
		if err != nil {
			fmt.Println("error writing to pidfile:", err.Error())
			os.Exit(1)
		}
		f.Close()
	}

	if config.Admin_addr != "" {
		go func() {
			err := adminListener(config.Admin_addr)
			if err != nil {
				fmt.Println("Error listening:", err.Error())
				os.Exit(1)
			}
		}()
	}

	if config.Http_addr != "" {
		go HttpListener(config.Http_addr, table)
	}

	if err := goagain.AwaitSignals(l); nil != err {
		log.Error(err.Error())
		os.Exit(1)
	}
}
