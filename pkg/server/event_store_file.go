package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type eventRecordJSON struct {
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	Repo        string    `json:"repo"`
	Summary     string    `json:"summary"`
	Delivery    string    `json:"delivery,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
}

func resolveEventFilePath() string {
	if v, ok := os.LookupEnv("UNISTAR_MCP_EVENT_FILE"); ok {
		v = strings.TrimSpace(v)
		switch strings.ToLower(v) {
		case "", "off", "memory", "disabled", "disable":
			return ""
		}
		return v
	}
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "unistar-mcp", "events.jsonl")
}

func (es *eventStore) loadFromFile() {
	if es.filePath == "" {
		return
	}
	data, err := os.ReadFile(es.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logrus.Warnf("event store: read %s: %v", es.filePath, err)
		}
		return
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxWebhookBodyBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row eventRecordJSON
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		es.events = append(es.events, eventRecord(row))
		if len(es.events) > es.capacity {
			es.events = es.events[len(es.events)-es.capacity:]
		}
	}
	if err := sc.Err(); err != nil {
		logrus.Warnf("event store: scan %s: %v", es.filePath, err)
	}
}

func (es *eventStore) persistLocked() {
	if es.filePath == "" {
		return
	}
	dir := filepath.Dir(es.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logrus.Warnf("event store: mkdir %s: %v", dir, err)
		return
	}
	var buf bytes.Buffer
	for _, ev := range es.events {
		row := eventRecordJSON(ev)
		b, err := json.Marshal(row)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	tmp := es.filePath + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		logrus.Warnf("event store: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, es.filePath); err != nil {
		logrus.Warnf("event store: rename %s: %v", es.filePath, err)
		_ = os.Remove(tmp)
	}
}

func (es *eventStore) filePathHint() string {
	if es.filePath == "" {
		return ""
	}
	return es.filePath
}
