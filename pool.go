// Copyright (c) 2018 Andy Pan
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
// of the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
//The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
//THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED,
// INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A
// PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT
// HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
// OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
// SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
package ants

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type sig struct{}

type f func()

// Pool accept the tasks from client,it will limit the total
// of goroutines to a given number by recycling goroutines.
type Pool struct {
	// Capacity of the pool.
	capacity int32

	// The number of the currently running goroutines.
	running int32

	// Signal is used to notice pool there are available
	// workers which can be sent to work.
	freeSignal chan sig

	// A slice that store the available workers.
	workers []*Worker

	workerPool sync.Pool

	// It is used to notice the pool to closed itself.
	release chan sig

	lock sync.Mutex

	// It is used to confirm whether this pool has been closed.
	closed int32
}

func NewPool(size int) (*Pool, error) {
	if size <= 0 {
		return nil, PoolSizeInvalidError
	}
	p := &Pool{
		capacity:   int32(size),
		freeSignal: make(chan sig, math.MaxInt32),
		release:    make(chan sig),
		closed:     0,
	}

	return p, nil
}

//-------------------------------------------------------------------------

// scanAndClean is a goroutine who will periodically clean up
// after it is noticed that this pool is closed.
func (p *Pool) scanAndClean() {
	ticker := time.NewTicker(DEFAULT_CLEAN_INTERVAL_TIME * time.Second)
	go func() {
		ticker.Stop()
		for range ticker.C {
			if atomic.LoadInt32(&p.closed) == 1 {
				p.lock.Lock()
				for _, w := range p.workers {
					w.stop()
				}
				p.lock.Unlock()
			}
		}
	}()
}

// Push submit a task to pool
func (p *Pool) Push(task f) error {
	if atomic.LoadInt32(&p.closed) == 1 {
		return PoolClosedError
	}
	w := p.getWorker()
	w.sendTask(task)
	return nil
}

// Running returns the number of the currently running goroutines
func (p *Pool) Running() int {
	return int(atomic.LoadInt32(&p.running))
}

// Free returns the available goroutines to work
func (p *Pool) Free() int {
	return int(atomic.LoadInt32(&p.capacity) - atomic.LoadInt32(&p.running))
}

// Cap returns the capacity of this pool
func (p *Pool) Cap() int {
	return int(atomic.LoadInt32(&p.capacity))
}

// Release Closed this pool
func (p *Pool) Release() error {
	p.lock.Lock()
	atomic.StoreInt32(&p.closed, 1)
	close(p.release)
	p.lock.Unlock()
	return nil
}

// Resize change the capacity of this pool
func (p *Pool) ReSize(size int) {
	atomic.StoreInt32(&p.capacity, int32(size))
}

//-------------------------------------------------------------------------

// getWorker returns a available worker to run the tasks.
func (p *Pool) getWorker() *Worker {
	var w *Worker
	waiting := false

	p.lock.Lock()
	workers := p.workers
	n := len(workers) - 1
	if n < 0 {
		if p.running >= p.capacity {
			waiting = true
		}
	} else {
		w = workers[n]
		workers[n] = nil
		p.workers = workers[:n]
	}
	p.lock.Unlock()

	if waiting {
		<-p.freeSignal
		for {
			p.lock.Lock()
			workers = p.workers
			l := len(workers) - 1
			if l < 0 {
				p.lock.Unlock()
				continue
			}
			w = workers[l]
			workers[l] = nil
			p.workers = workers[:l]
			p.lock.Unlock()
			break
		}
	} else {
		wp := p.workerPool.Get()
		if wp == nil {
			w = &Worker{
				pool: p,
				task: make(chan f),
			}
			w.run()
			atomic.AddInt32(&p.running, 1)
		} else {
			w = wp.(*Worker)
		}
	}
	return w
}

// putWorker puts a worker back into free pool, recycling the goroutines.
func (p *Pool) putWorker(worker *Worker) {
	p.workerPool.Put(worker)
	p.lock.Lock()
	p.workers = append(p.workers, worker)
	p.lock.Unlock()
	p.freeSignal <- sig{}
}
