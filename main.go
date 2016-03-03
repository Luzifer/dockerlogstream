package main

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/Luzifer/rconfig"
	"github.com/fluent/fluent-logger-golang/fluent"
	"github.com/fsouza/go-dockerclient"
	"github.com/robertkrimen/otto"
)

var (
	ProjectVersion = "dev"
	ProjectBuild   = "HEAD"

	logstream chan *message
	cfg       config
	client    *docker.Client
	err       error

	jsVM            = otto.New()
	jsLineConverter *otto.Script

	containerCache = map[string]*docker.Container{}
)

type config struct {
	PapertrailEndpoint string `flag:"papertrail-endpoint" description:"[DEPRECATED] Logging target in PaperTrail (TCP, Plain)"`
	DockerAPI          string `flag:"docker-endpoint" default:"/var/run/docker.sock" description:"Address of docker endpoint to use"`
	Testing            bool   `flag:"testing" default:"false" description:"Do not stream but write to STDOUT"`
	LineConverter      string `flag:"line-converter" default:"lineconverter.js" description:"Sets the JavaScript to compile the log line to be sent"`
	SysLogEndpoint     string `flag:"endpoint" description:"TCP/plain capable syslog endpoint (PaperTrail, Loggly, ...)"`
	ListenAddress      string `flag:"listen" default:"localhost:24224" description:"Listen address for fluentd protocol"`
}

func main() {
	rconfig.Parse(&cfg)

	// Transistion for deprecated --papertrail-endpoint parameter
	if cfg.SysLogEndpoint == "" && cfg.PapertrailEndpoint != "" {
		cfg.SysLogEndpoint = cfg.PapertrailEndpoint
	}

	logstream = make(chan *message, 5000)

	jsLineConverter, err = jsVM.Compile(cfg.LineConverter, nil)
	if err != nil {
		log.Fatalf("Unable to parse line converter script: %s", err)
	}

	log.Printf("Started dockerlogstream %s (%s)", ProjectVersion, ProjectBuild)

	// Connect to Docker socket
	client, err = docker.NewClient("unix://" + cfg.DockerAPI)
	if err != nil {
		log.Fatalf("Unable to connect to Docker daemon: %s", err)
	}

	if cfg.Testing {
		// Log to STDOUT instead of streaming
		ta := TestAdapter{}
		go ta.Stream(logstream)
	} else {
		// Create sending part of the logger
		sl, err := NewSyslogAdapter(cfg.SysLogEndpoint)
		if err != nil {
			log.Fatalf("Unable to create logging adapter: %s", err)
		}
		go sl.Stream(logstream)
	}

	fluentServer, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		log.Fatalf("Unable to listen: %s", err)
	}

	for {
		conn, err := fluentServer.Accept()
		if err != nil {
			log.Fatalf("Unable to accept tcp connection: %s", err)
		}

		go handleFluentdForwardConnection(conn)
	}
}

func handleFluentdForwardConnection(c net.Conn) {
	defer c.Close()
	buffer := bytes.NewBuffer([]byte{})

	for {
		tmp := make([]byte, 256)
		c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))

		n, err := c.Read(tmp)
		if err != nil {
			if oe, ok := err.(*net.OpError); ok {
				if oe.Temporary() {
					continue
				}
			}
			break
		}
		buffer.Write(tmp[:n])

		for {
			var (
				msg         fluent.Message
				newBytes    []byte
				storedBytes = buffer.Bytes()
			)

			newBytes, err = msg.UnmarshalMsg(storedBytes)
			if err != nil {
				buffer = bytes.NewBuffer(storedBytes)
				break
			}

			buffer = bytes.NewBuffer(newBytes)
			if err := handleLogMessage(msg); err != nil {
				log.Printf("Unable to process log message: %#v -- ERR=%s", msg, err)
			}
		}
	}
}

func handleLogMessage(msg fluent.Message) error {
	data := msg.Record.(map[string]interface{})
	container, err := getContainerInformation(data["container_id"].(string))
	if err != nil {
		return fmt.Errorf("Unable to fetch container information: %s", err)
	}

	logstream <- &message{
		Container: container,
		Data:      strings.TrimSpace(data["log"].(string)),
		Time:      time.Unix(msg.Time, 0),
	}

	return nil
}

func getContainerInformation(id string) (*docker.Container, error) {
	if c, ok := containerCache[id]; ok {
		return c, nil
	}

	container, err := client.InspectContainer(id)

	containerCache[id] = container
	return container, err
}
