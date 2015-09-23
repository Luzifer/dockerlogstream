package main

import (
	"log"
	"testing"
	"time"

	"github.com/fsouza/go-dockerclient"
	"github.com/robertkrimen/otto"
)

const benchmarkLogFormatScript = `
container_name = message.Container.Names[0].substring(1)
result = "<22> " + message.Time.Format("Jan 2 15:04:05") + " " + hostname + " " + container_name + ": " + message.Data;
`

func BenchmarkFormatLogLine(b *testing.B) {
	jsVM = otto.New()
	jsLineConverter, err = jsVM.Compile("", benchmarkLogFormatScript)
	if err != nil {
		log.Fatalf("Unable to parse line converter script: %s", err)
		b.Fail()
	}

	m := &message{
		Container: docker.APIContainers{Names: []string{"/testcontainer"}},
		Data:      "I am a random log line with a few characters like I might be generated by some random program",
		Time:      time.Now(),
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := formatLogLine(m); err != nil {
			b.Fail()
		}
	}
}