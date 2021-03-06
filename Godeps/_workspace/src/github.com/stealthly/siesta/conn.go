/* Licensed to the Apache Software Foundation (ASF) under one or more
contributor license agreements.  See the NOTICE file distributed with
this work for additional information regarding copyright ownership.
The ASF licenses this file to You under the Apache License, Version 2.0
(the "License"); you may not use this file except in compliance with
the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License. */

package siesta

import (
	"net"
	"sync"
	"time"
)

type connectionPool struct {
	connectStr       string
	size             int
	conns            int
	keepAlive        bool
	keepAlivePeriod  time.Duration
	connections      []*net.TCPConn
	lock             sync.Mutex
	connReleasedCond *sync.Cond
}

func newConnectionPool(connectStr string, size int, keepAlive bool, keepAlivePeriod time.Duration) *connectionPool {
	pool := &connectionPool{
		connectStr:      connectStr,
		size:            size,
		conns:           0,
		keepAlive:       keepAlive,
		keepAlivePeriod: keepAlivePeriod,
		connections:     make([]*net.TCPConn, 0),
	}

	pool.connReleasedCond = sync.NewCond(&pool.lock)

	return pool
}

func (this *connectionPool) Borrow() (conn *net.TCPConn, err error) {
	inLock(&this.lock, func() {
		for this.conns >= this.size && len(this.connections) == 0 {
			this.connReleasedCond.Wait()
		}

		if len(this.connections) > 0 {
			conn = this.connections[0]
			this.connections = this.connections[1:]
		} else {
			conn, err = this.connect()
			if err != nil {
				return
			}
			this.conns++
		}
	})
	return conn, err
}

func (this *connectionPool) Return(conn *net.TCPConn) {
	inLock(&this.lock, func() {
		if len(this.connections) < this.conns {
			this.connections = append(this.connections, conn)
			this.connReleasedCond.Broadcast()
		}
	})
}

func (this *connectionPool) connect() (*net.TCPConn, error) {
	addr, err := net.ResolveTCPAddr("tcp", this.connectStr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTCP("tcp", nil, addr)
	if err != nil {
		return nil, err
	}

	if this.keepAlive {
		conn.SetKeepAlive(this.keepAlive)
		conn.SetKeepAlivePeriod(this.keepAlivePeriod)
	}

	return conn, nil
}
