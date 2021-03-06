package ring

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net"
	"testing"
	"time"
)

func newRingConn(conn net.Conn) *ringConn {
	return &ringConn{
		state:  _STATE_CONNECTED,
		conn:   conn,
		reader: newTimeoutReader(conn, 16*1024, 2*time.Second),
		writer: newTimeoutWriter(conn, 16*1024, 2*time.Second),
	}
}

// Mock up a bunch of stuff

func newTestRing() (Ring, Node, Node) {
	b := NewBuilder()
	b.SetReplicaCount(3)
	nA := b.AddNode(true, 1, nil, []string{"127.0.0.1:9999"}, "", []byte("Conf"))
	nB := b.AddNode(true, 1, nil, []string{"127.0.0.1:8888"}, "", []byte("Conf"))
	r := b.Ring()
	r.SetLocalNode(nA.ID())
	return r, nA, nB
}

var testMsg = []byte("Testing")
var testStr = "Testing"

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

func TestTCPMsgRingIsMsgRing(t *testing.T) {
	r, _, _ := newTestRing()
	tmr := NewTCPMsgRing(r)
	func(mr MsgRing) {}(tmr)
}

func Test_NewTCPMsgRing(t *testing.T) {
	r, _, _ := newTestRing()
	msgring := NewTCPMsgRing(r)
	if msgring.Ring().LocalNode().Address(0) != "127.0.0.1:9999" {
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

func Test_handle(t *testing.T) {
	log.SetOutput(ioutil.Discard)
	conn := new(testConn)
	binary.Write(&conn.readBuf, binary.BigEndian, uint64(1))
	binary.Write(&conn.readBuf, binary.BigEndian, uint64(7))
	conn.readBuf.WriteString(testStr)
	r, _, _ := newTestRing()
	msgring := NewTCPMsgRing(r)
	msgring.SetMsgHandler(1, test_stringmarshaller)
	msgring.handleForever(newRingConn(conn))
}

func Test_MsgToNode(t *testing.T) {
	conn := new(testConn)
	r, _, nB := newTestRing()
	msgring := NewTCPMsgRing(r)
	msgring.conns[nB.Address(0)] = newRingConn(conn)
	msg := TestMsg{}
	msgring.MsgToNode(nB.ID(), &msg)
	var msgtype uint64
	binary.Read(&conn.writeBuf, binary.BigEndian, &msgtype)
	if int(msgtype) != 1 {
		t.Error("Message type not written correctly")
	}
	var msgsize uint64
	binary.Read(&conn.writeBuf, binary.BigEndian, &msgsize)
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
	r, _, nB := newTestRing()
	msgring := NewTCPMsgRing(r)
	msgring.conns[nB.Address(0)] = newRingConn(conn)
	msg := TestMsg{}
	retch := make(chan struct{})
	go msgring.msgToNodeChan(&msg, nB, retch)
	<-retch
	var msgtype uint64
	binary.Read(&conn.writeBuf, binary.BigEndian, &msgtype)
	if int(msgtype) != 1 {
		t.Error("Message type not written correctly")
	}
	var msgsize uint64
	binary.Read(&conn.writeBuf, binary.BigEndian, &msgsize)
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
	r, _, nB := newTestRing()
	msgring := NewTCPMsgRing(r)
	msgring.conns[nB.Address(0)] = newRingConn(conn)
	msg := TestMsg{}
	msgring.MsgToOtherReplicas(r.Version(), uint32(1), &msg)
	var msgtype uint64
	err := binary.Read(&conn.writeBuf, binary.BigEndian, &msgtype)
	if err != nil {
		t.Error(err)
	}
	if int(msgtype) != 1 {
		t.Errorf("Message type not written correctly was %d", msgtype)
	}
	var msgsize uint64
	binary.Read(&conn.writeBuf, binary.BigEndian, &msgsize)
	if msgsize != 7 {
		t.Error("Incorrect message size")
	}
	msgcontent := make([]byte, 7)
	conn.writeBuf.Read(msgcontent)
	if !bytes.Equal(msgcontent, testMsg) {
		t.Error("Incorrect message contents")
	}
}
