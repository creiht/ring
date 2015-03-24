package ring

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// Mock up a bunch of stuff

func newCommsTestRing() *Ring {
	b := NewBuilder(3)
	b.Add(&Node{ID: 1, Addresses: []string{"127.0.0.1:9999"}})
	b.Add(&Node{ID: 2, Addresses: []string{"127.0.0.1:9999"}})
	return b.Ring(1)
}

var testMsg []byte = []byte("Testing")
var testStr string = "Testing"

type TestMsg struct {
}

func (m *TestMsg) MsgType() uint64 {
	return 1
}

func (m *TestMsg) MsgLength() uint64 {
	return 7
}

func (m *TestMsg) WriteContent(writer io.Writer) (uint64, error) {
	count, err := writer.Write(testMsg)
	return uint64(count), err
}

func (m *TestMsg) Done() {
	return
}

// Following mock stuff borrowed from golang.org/src/net/http/serve_test.go
type dummyAddr string

func (a dummyAddr) Network() string {
	return string(a)
}

func (a dummyAddr) String() string {
	return string(a)
}

type noopConn struct{}

func (noopConn) LocalAddr() net.Addr                { return dummyAddr("local-addr") }
func (noopConn) RemoteAddr() net.Addr               { return dummyAddr("remote-addr") }
func (noopConn) SetDeadline(t time.Time) error      { return nil }
func (noopConn) SetReadDeadline(t time.Time) error  { return nil }
func (noopConn) SetWriteDeadline(t time.Time) error { return nil }

type testConn struct {
	readBuf  bytes.Buffer
	writeBuf bytes.Buffer
	noopConn
}

func (c *testConn) Read(b []byte) (int, error) {
	return c.readBuf.Read(b)
}

func (c *testConn) Write(b []byte) (int, error) {
	return c.writeBuf.Write(b)
}

func (c *testConn) Close() error {
	return nil
}

/***** Actual tests start here *****/

func Test_NewTCPMsgRing(t *testing.T) {
	r := newCommsTestRing()
	msgring := NewTCPMsgRing(r)
	if msgring.Ring().LocalNode().ID != 1 {
		t.Error("Error initializing TCPMsgRing")
	}
}

func test_stringmarshaller(reader io.Reader, size uint64) (uint64, error) {
	buf := make([]byte, size)
	c, err := reader.Read(buf)
	if !bytes.Equal(buf, testMsg) {
		err = errors.New("Unmarshaller didn't read the correct value")
	}
	return uint64(c), err
}

func isTimeout(err error) bool {
	e, ok := err.(net.Error)
	return ok && e.Timeout()
}

func Test_ReadTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reader := NewTimeoutReader(c)
	reader.Timeout = -3 * time.Second
	_, err = reader.ReadByte()
	if err == nil {
		t.Error("Read didn't timeout")
	} else if !isTimeout(err) {
		t.Error("Error wasn't a timeout: ", err)
	}
	reader.Timeout = 10 * time.Millisecond
	_, err = reader.ReadByte()
	if err == nil {
		t.Error("Read didn't timeout")
	} else if !isTimeout(err) {
		t.Error("Error wasn't a timeout: ", err)
	}
}

func Test_WriteTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.DialTCP("tcp", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	writer := NewTimeoutWriter(c)
	writer.Timeout = -3 * time.Second
	writer.Write([]byte("Test"))
	err = writer.Flush()
	if err == nil {
		t.Error("Write didn't timeout")
	} else if !isTimeout(err) {
		t.Error("Error wasn't a timeout: ", err)
	}
	writer.Timeout = 10 * time.Millisecond
	err = writer.Flush()
	if err == nil {
		t.Error("Read didn't timeout")
	} else if !isTimeout(err) {
		t.Error("Error wasn't a timeout: ", err)
	}
}

func Test_handle(t *testing.T) {
	conn := new(testConn)
	binary.Write(&conn.readBuf, binary.LittleEndian, uint64(1))
	binary.Write(&conn.readBuf, binary.LittleEndian, uint64(7))
	conn.readBuf.WriteString(testStr)
	r := newCommsTestRing()
	msgring := NewTCPMsgRing(r)
	msgring.SetMsgHandler(1, test_stringmarshaller)
	err := msgring.handle(conn)
	if err != nil && err != io.EOF {
		t.Error(err)
	}
}

func Test_MsgToNode(t *testing.T) {
	conn := new(testConn)
	r := newCommsTestRing()
	msgring := NewTCPMsgRing(r)
	addr := msgring.ring.Node(uint64(1)).Addresses[0]
	msgring.conns[addr] = NewRingConn(conn)
	msg := TestMsg{}
	msgring.MsgToNode(uint64(1), &msg)
	var msgtype uint64
	binary.Read(&conn.writeBuf, binary.LittleEndian, &msgtype)
	if int(msgtype) != 1 {
		t.Error("Message type not written correctly")
	}
	var msgsize uint64
	binary.Read(&conn.writeBuf, binary.LittleEndian, &msgsize)
	if msgsize != 7 {
		t.Error("Incorrect message size")
	}
	msgcontent := make([]byte, 7)
	conn.writeBuf.Read(msgcontent)
	if !bytes.Equal(msgcontent, testMsg) {
		t.Error("Incorrect message contents")
	}
}

func Test_MsgToNodeChan(t *testing.T) {
	conn := new(testConn)
	r := newCommsTestRing()
	msgring := NewTCPMsgRing(r)
	addr := msgring.ring.Node(uint64(1)).Addresses[0]
	msgring.conns[addr] = NewRingConn(conn)
	msg := TestMsg{}
	retch := make(chan struct{})
	go msgring.MsgToNodeChan(uint64(1), &msg, retch)
	<-retch
	var msgtype uint64
	binary.Read(&conn.writeBuf, binary.LittleEndian, &msgtype)
	if int(msgtype) != 1 {
		t.Error("Message type not written correctly")
	}
	var msgsize uint64
	binary.Read(&conn.writeBuf, binary.LittleEndian, &msgsize)
	if msgsize != 7 {
		t.Error("Incorrect message size")
	}
	msgcontent := make([]byte, 7)
	conn.writeBuf.Read(msgcontent)
	if !bytes.Equal(msgcontent, testMsg) {
		t.Error("Incorrect message contents")
	}
}

func Test_MsgToOtherReplicas(t *testing.T) {
	conn := new(testConn)
	r := newCommsTestRing()
	msgring := NewTCPMsgRing(r)
	addr := msgring.ring.Node(uint64(1)).Addresses[0]
	msgring.conns[addr] = NewRingConn(conn)
	msg := TestMsg{}
	msgring.MsgToOtherReplicas(int64(1), uint32(1), &msg)
	// The following should be written twice
	for i := 0; i < 2; i++ {
		var msgtype uint64
		binary.Read(&conn.writeBuf, binary.LittleEndian, &msgtype)
		if int(msgtype) != 1 {
			t.Error("Message type not written correctly")
		}
		var msgsize uint64
		binary.Read(&conn.writeBuf, binary.LittleEndian, &msgsize)
		if msgsize != 7 {
			t.Error("Incorrect message size")
		}
		msgcontent := make([]byte, 7)
		conn.writeBuf.Read(msgcontent)
		if !bytes.Equal(msgcontent, testMsg) {
			t.Error("Incorrect message contents")
		}
	}
}
