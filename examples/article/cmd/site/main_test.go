package main

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
)

func TestParseServeConfig(t *testing.T) {
	config, err := parseServeConfig([]string{"serve", "--database", "article.sqlite3"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != defaultListenAddress || config.database != "article.sqlite3" {
		t.Fatalf("config = %#v", config)
	}
	config, err = parseServeConfig([]string{"serve", "--listen", "127.0.0.1:0", "--database", "file:article.sqlite3"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != "127.0.0.1:0" || config.database != "file:article.sqlite3" {
		t.Fatalf("explicit config = %#v", config)
	}
}

func TestParseServeConfigRejectsInvalidArguments(t *testing.T) {
	tests := [][]string{
		nil,
		{"run", "--database", "article.sqlite3"},
		{"serve"},
		{"serve", "--database", "article.sqlite3", "unexpected"},
		{"serve", "--listen", " ", "--database", "article.sqlite3"},
	}
	for _, arguments := range tests {
		if _, err := parseServeConfig(arguments, &bytes.Buffer{}); err == nil {
			t.Errorf("parseServeConfig(%q) error = nil", arguments)
		}
	}
}

func TestRunRejectsNilContextBeforeSideEffects(t *testing.T) {
	if err := run(nil, []string{"serve", "--database", "article.sqlite3"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("run(nil) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(canceled, []string{"serve", "--database", "article.sqlite3"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("run(canceled) error = nil")
	}
}

func TestRunClosesListenerOnceWhenContextCancelsBeforeServeOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var listener *countingListener
	listen := func(network, address string) (net.Listener, error) {
		opened, err := net.Listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listener = &countingListener{Listener: opened}
		return listener, nil
	}
	stdout := cancelWriter{cancel: cancel}
	err := runWithListener(
		ctx,
		[]string{"serve", "--listen", "127.0.0.1:0", "--database", "file:site-cancel-window?mode=memory&cache=shared"},
		stdout,
		&bytes.Buffer{},
		listen,
	)
	if err != nil {
		t.Fatalf("runWithListener() error = %v", err)
	}
	if listener == nil {
		t.Fatal("listener was not created")
	}
	if closes := listener.closes.Load(); closes != 1 {
		t.Fatalf("listener Close() calls = %d, want 1", closes)
	}
}

type cancelWriter struct {
	cancel context.CancelFunc
}

func (w cancelWriter) Write(payload []byte) (int, error) {
	w.cancel()
	return len(payload), nil
}

type countingListener struct {
	net.Listener
	closes atomic.Int32
}

func (l *countingListener) Close() error {
	l.closes.Add(1)
	return l.Listener.Close()
}
