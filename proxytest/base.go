// Copyright 2020 Tetrate
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proxytest

import (
	"log"
	"reflect"
	"sync"
	"unsafe"

	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/rawhostcall"
	"github.com/tetratelabs/proxy-wasm-go-sdk/proxywasm/types"
)

var hostMux = sync.Mutex{}

type baseHost struct {
	rawhostcall.DefaultProxyWAMSHost
	logs       [types.LogLevelMax][]string
	tickPeriod uint32

	queues      map[uint32][][]byte
	queueNameID map[string]uint32

	sharedDataKVS map[string]*sharedData

	metricIDToValue map[uint32]uint64
	metricIDToType  map[uint32]types.MetricType
	metricNameToID  map[string]uint32
}

type sharedData struct {
	data []byte
	cas  uint32
}

func newBaseHost() *baseHost {
	return &baseHost{
		queues:          map[uint32][][]byte{},
		queueNameID:     map[string]uint32{},
		sharedDataKVS:   map[string]*sharedData{},
		metricIDToValue: map[uint32]uint64{},
		metricIDToType:  map[uint32]types.MetricType{},
		metricNameToID:  map[string]uint32{},
	}
}

func (b *baseHost) ProxyLog(logLevel types.LogLevel, messageData *byte, messageSize int) types.Status {
	str := *(*string)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(messageData)),
		Len:  messageSize,
		Cap:  messageSize,
	}))

	log.Printf("proxy_log: %s", str)
	// TODO: exit if loglevel == fatal?

	b.logs[logLevel] = append(b.logs[logLevel], str)
	return types.StatusOK
}

func (b *baseHost) GetLogs(level types.LogLevel) []string {
	if level >= types.LogLevelMax {
		log.Fatalf("invalid log level: %d", level)
	}
	return b.logs[level]
}

func (b *baseHost) getBuffer(bt types.BufferType, start int, maxSize int,
	returnBufferData **byte, returnBufferSize *int) types.Status {

	// TODO: should implement http callout response
	panic("unimplemented")
}

func (b *baseHost) ProxySetTickPeriodMilliseconds(period uint32) types.Status {
	b.tickPeriod = period
	return types.StatusOK
}

func (b *baseHost) GetTickPeriod() uint32 {
	return b.tickPeriod
}

// TODO: implement http callouts

func (b *baseHost) ProxyRegisterSharedQueue(nameData *byte, nameSize int, returnID *uint32) types.Status {
	name := *(*string)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(nameData)),
		Len:  nameSize,
		Cap:  nameSize,
	}))

	if id, ok := b.queueNameID[name]; ok {
		*returnID = id
		return types.StatusOK
	}

	id := uint32(len(b.queues))
	b.queues[id] = [][]byte{}
	b.queueNameID[name] = id
	*returnID = id
	return types.StatusOK
}

func (b *baseHost) ProxyDequeueSharedQueue(queueID uint32, returnValueData **byte, returnValueSize *int) types.Status {
	queue, ok := b.queues[queueID]
	if !ok {
		log.Printf("queue %d is not found", queueID)
		return types.StatusNotFound
	} else if len(queue) == 0 {
		log.Printf("queue %d is empty", queueID)
		return types.StatusEmpty
	}

	data := queue[0]
	*returnValueData = &data[0]
	*returnValueSize = len(data)
	b.queues[queueID] = queue[1:]
	return types.StatusOK
}

func (b *baseHost) ProxyEnqueueSharedQueue(queueID uint32, valueData *byte, valueSize int) types.Status {
	queue, ok := b.queues[queueID]
	if !ok {
		log.Printf("queue %d is not found", queueID)
		return types.StatusNotFound
	}

	b.queues[queueID] = append(queue, *(*[]byte)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(valueData)),
		Len:  valueSize,
		Cap:  valueSize,
	})))

	// TODO: should call OnQueueReady?

	return types.StatusOK
}

func (b *baseHost) GetQueueSize(queueID uint32) int {
	return len(b.queues[queueID])
}

func (b *baseHost) ProxyGetSharedData(keyData *byte, keySize int,
	returnValueData **byte, returnValueSize *int, returnCas *uint32) types.Status {
	key := *(*string)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(keyData)),
		Len:  keySize,
		Cap:  keySize,
	}))

	value, ok := b.sharedDataKVS[key]
	if !ok {
		return types.StatusNotFound
	}

	*returnValueSize = len(value.data)
	*returnValueData = &value.data[0]
	*returnCas = value.cas
	return types.StatusOK
}

func (b *baseHost) ProxySetSharedData(keyData *byte, keySize int,
	valueData *byte, valueSize int, cas uint32) types.Status {
	key := *(*string)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(keyData)),
		Len:  keySize,
		Cap:  keySize,
	}))
	value := *(*[]byte)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(valueData)),
		Len:  valueSize,
		Cap:  valueSize,
	}))

	prev, ok := b.sharedDataKVS[key]
	if !ok {
		b.sharedDataKVS[key] = &sharedData{
			data: value,
			cas:  cas + 1,
		}
		return types.StatusOK
	}

	if prev.cas != cas {
		return types.StatusCasMismatch
	}

	b.sharedDataKVS[key].cas = cas + 1
	b.sharedDataKVS[key].data = value
	return types.StatusOK
}

func (b *baseHost) ProxyDefineMetric(metricType types.MetricType,
	metricNameData *byte, metricNameSize int, returnMetricIDPtr *uint32) types.Status {
	name := *(*string)(unsafe.Pointer(&reflect.SliceHeader{
		Data: uintptr(unsafe.Pointer(metricNameData)),
		Len:  metricNameSize,
		Cap:  metricNameSize,
	}))
	id, ok := b.metricNameToID[name]
	if !ok {
		id = uint32(len(b.metricNameToID))
		b.metricNameToID[name] = id
		b.metricIDToValue[id] = 0
		b.metricIDToType[id] = metricType
	}
	*returnMetricIDPtr = id
	return types.StatusOK
}

func (b *baseHost) ProxyIncrementMetric(metricID uint32, offset int64) types.Status {
	// TODO: check metric type

	val, ok := b.metricIDToValue[metricID]
	if !ok {
		return types.StatusBadArgument
	}

	b.metricIDToValue[metricID] = val + uint64(offset)
	return types.StatusOK
}

func (b *baseHost) ProxyRecordMetric(metricID uint32, value uint64) types.Status {
	// TODO: check metric type

	_, ok := b.metricIDToValue[metricID]
	if !ok {
		return types.StatusBadArgument
	}
	b.metricIDToValue[metricID] = value
	return types.StatusOK
}

func (b *baseHost) ProxyGetMetric(metricID uint32, returnMetricValue *uint64) types.Status {
	value, ok := b.metricIDToValue[metricID]
	if !ok {
		return types.StatusBadArgument
	}
	*returnMetricValue = value
	return types.StatusOK
}