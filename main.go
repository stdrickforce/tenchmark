package main

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"

	. "github.com/stdrickforce/thriftgo/protocol"
	. "github.com/stdrickforce/thriftgo/thrift"
	. "github.com/stdrickforce/thriftgo/transport"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

type Case func(proto Protocol) error

var (
	wg sync.WaitGroup
)

func call(name string, args ...interface{}) Case {
	var writeMessageBody = func(proto Protocol) (err error) {
		if err = proto.WriteStructBegin("whatever"); err != nil {
			return
		}
		for i, arg := range args {
			index := int16(i + 1)
			switch v := arg.(type) {
			case int16:
				err = proto.WriteFieldBegin("i16", T_I16, index)
				err = proto.WriteI16(v)
			case int32:
				err = proto.WriteFieldBegin("i32", T_I32, index)
				err = proto.WriteI32(v)
			case int64:
				err = proto.WriteFieldBegin("i64", T_I64, index)
				err = proto.WriteI64(v)
			case string:
				err = proto.WriteFieldBegin("string", T_STRING, index)
				err = proto.WriteString(v)
			default:
				err = errors.New("unsupport type")
			}
			if err != nil {
				return
			}
		}
		if err = proto.WriteFieldStop(); err != nil {
			return
		}
		if err = proto.WriteStructEnd(); err != nil {
			return
		}
		if err = proto.WriteMessageEnd(); err != nil {
			return
		}
		if err = proto.Flush(); err != nil {
			return
		}
		return
	}

	return func(proto Protocol) (err error) {
		if err = proto.WriteMessageBegin(name, T_CALL, 0); err != nil {
			return
		}
		if err = writeMessageBody(proto); err != nil {
			return
		}
		if _, _, _, err = proto.ReadMessageBegin(); err != nil {
			return
		}
		if err = proto.Skip(T_STRUCT); err != nil {
			return
		}
		if err = proto.ReadMessageEnd(); err != nil {
			return
		}
		return
	}
}

type Processor struct {
	service string
	pf      ProtocolFactory
	tf      TransportFactory
	tw      TransportWrapper
	fn      Case
	ch      chan int
}

func (p *Processor) process(gid, count int) {
	defer wg.Done()

	var (
		trans Transport
		proto Protocol
	)

	trans = p.tf.GetTransport()
	trans = p.tw.GetTransport(trans)
	proto = p.pf.GetProtocol(trans)

	if p.service != "" {
		proto = NewMultiplexedProtocol(proto, p.service)
	}

	if err := trans.Open(); err != nil {
		panic(err)
	}
	defer trans.Close()

	for i := 0; i < count; i++ {
		snano := time.Now().UnixNano()
		if err := p.fn(proto); err != nil {
			fmt.Println(gid, err)
			return
		}
		duration := time.Now().UnixNano() - snano
		p.ch <- int(duration / 1000)
	}
}

func sort(values []int, l, r int) {
	if l >= r {
		return
	}

	pivot := values[l]
	i := l + 1

	for j := l + 1; j <= r; j++ {
		if pivot > values[j] {
			values[i], values[j] = values[j], values[i]
			i++
		}
	}

	values[l], values[i-1] = values[i-1], pivot

	sort(values, l, i-2)
	sort(values, i, r)
}

func collect(processor *Processor, pipe chan<- string) {
	defer close(pipe)

	snano := time.Now().UnixNano()

	var s = make([]int, 0)
	for duration := range processor.ch {
		s = append(s, duration)
	}

	dnano := time.Now().UnixNano() - snano

	l := len(s)
	sort(s, 0, l-1)

	v := func(denominator int) float64 {
		if denominator <= 0 {
			return float64(s[l-1]) / 1000
		} else {
			return float64(s[l*(denominator-1)/denominator-1]) / 1000
		}
	}

	var (
		duration = float64(dnano) / math.Pow(10, 9)
		qps      = float64(l) / duration
	)

	pipe <- fmt.Sprintf("%-24s%s", "Server Address:", *addr)
	pipe <- ""
	pipe <- fmt.Sprintf("%-24s%d", "Concurrency level:", *concurrency)
	pipe <- fmt.Sprintf("%-24s%.3f seconds", "Time taken for tests:", duration)
	pipe <- fmt.Sprintf("%-24s%d", "Complete requests:", l)
	pipe <- fmt.Sprintf("%-24s%d", "Failed requests:", *requests-l)
	pipe <- fmt.Sprintf("%-24s%.2f [#/sec] (mean)", "Request per second:", qps)
	pipe <- ""
	pipe <- "Percentage of the requests served within a certain time (ms)"
	pipe <- fmt.Sprintf("%4d%% %8.2f", 50, v(2))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 66, v(3))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 75, v(4))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 80, v(5))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 90, v(10))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 95, v(20))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 98, v(50))
	pipe <- fmt.Sprintf("%4d%% %8.2f", 99, v(100))
	pipe <- fmt.Sprintf("%4d%% %8.2f (longest request)", 100, v(-1))
}

var (
	requests          = kingpin.Flag("requests", "Number of requests to perform").Short('n').Default("100").Int()
	concurrency       = kingpin.Flag("concurrency", "Number of multiple requests to make at a time").Short('c').Default("10").Int()
	path              = kingpin.Flag("path", "Http request path").Default("/").String()
	protocol          = kingpin.Flag("protocol", "Specify protocol factory").Default("binary").String()
	transport         = kingpin.Flag("transport", "Specify transport factory").Default("socket").String()
	transport_wrapper = kingpin.Flag("transport-wrapper", "Specify transport wrapper").Default("buffered").String()
	service           = kingpin.Flag("service", "Specify service name").String()

	addr = kingpin.Arg("addr", "Server addr").Default(":6000").String()
)

func get_transport_wrapper(name string) TransportWrapper {
	switch name {
	case "none":
		return TTransportWrapper
	case "buffered":
		return NewTBufferedTransportFactory(4096, 4096)
	case "framed":
		return NewTFramedTransportFactory(false, true)
	default:
		panic("invalid transport wrapper")
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	kingpin.Parse()

	if *concurrency <= 0 {
		panic("Invalid number of concurrency")
	}

	if *requests <= 0 {
		panic("Invalid number of requests")
	}

	var processor = &Processor{
		pf:      NewTBinaryProtocolFactory(true, true),
		tf:      NewTSocketFactory(*addr),
		tw:      get_transport_wrapper(*transport_wrapper),
		fn:      call("ping"),
		ch:      make(chan int, *concurrency*2),
		service: *service,
	}

	var pipe = make(chan string)
	go collect(processor, pipe)

	fmt.Printf("Benchmarking %v (be patient)......\n\n", *addr)

	quotient, remainder := *requests / *concurrency, *requests%*concurrency
	for i := 0; i < *concurrency; i++ {
		if i < remainder {
			go processor.process(i, quotient+1)
		} else {
			go processor.process(i, quotient)
		}
		wg.Add(1)
	}
	wg.Wait()

	close(processor.ch)

	for line := range pipe {
		fmt.Println(line)
	}
}