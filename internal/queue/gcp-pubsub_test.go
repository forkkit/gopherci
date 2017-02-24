package queue

import (
	"context"
	"encoding/gob"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
)

// TODO read from .env
const projectID = "gopherci-dev"

func TestGCPPubSubQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}
	// it appears some other routine maybe leaked
	// by the http client
	//defer leaktest.Check(t)() // ensure all goroutines exit

	var (
		ctx, cancel = context.WithCancel(context.Background())
		wg          sync.WaitGroup
		c           = make(chan interface{})
		topic       = fmt.Sprintf("%s-unit-tests-%v", defaultTopicName, time.Now().Unix())
	)
	q, err := NewGCPPubSubQueue(ctx, &wg, c, projectID, topic)
	if err != nil {
		t.Fatal("unexpected error:", err)
	}

	type S struct{ Job string }
	gob.Register(&S{})
	job := S{"unit-test-" + topic}
	q.Queue(job)

	have := <-c
	q.delete()

	concrete, ok := have.(*S)
	if !ok {
		t.Fatalf("have type: %T is not %T", have, &S{})
	}

	if !reflect.DeepEqual(*concrete, job) {
		t.Errorf("have (concrete): %#v, want: %#v", *concrete, job)
	}

	cancel()
}

func TestGCPPubSubQueue_timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	// Set cxnTimeout to a value that will be exceeded
	cxnTimeout = time.Millisecond

	var (
		ctx   = context.Background()
		wg    sync.WaitGroup
		c     = make(chan interface{})
		topic = fmt.Sprintf("%s-unit-tests-%v", defaultTopicName, time.Now().Unix())
	)
	_, err := NewGCPPubSubQueue(ctx, &wg, c, projectID, topic)

	have := errors.Cause(err)
	if want := context.DeadlineExceeded; have != want {
		t.Fatalf("have %v, want %v", have, want)
	}
}
