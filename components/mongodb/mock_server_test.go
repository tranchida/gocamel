package mongodb

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	wireOpReply = int32(1)
	wireOpQuery = int32(2004)
	wireOpMsg   = int32(2013)
)

type recordedMongoCommand struct {
	Name string
	Doc  bson.M
}

type mockMongoServer struct {
	ln       net.Listener
	handler  func(cmdName string, cmd bson.M) bson.D
	mu       sync.Mutex
	commands []recordedMongoCommand
	conns    []net.Conn
	nextID   atomic.Int32
	closed   atomic.Bool
	wg       sync.WaitGroup
}

func startMockMongoServer(t *testing.T, handler func(cmdName string, cmd bson.M) bson.D) (*mockMongoServer, *mongo.Client) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &mockMongoServer{
		ln:      ln,
		handler: handler,
	}
	srv.nextID.Store(1)

	srv.wg.Add(1)
	go srv.acceptLoop()

	uri := "mongodb://" + ln.Addr().String() + "/?directConnection=true"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(2*time.Second).
		SetConnectTimeout(2*time.Second))
	require.NoError(t, err)

	t.Cleanup(func() {
		discCtx, discCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer discCancel()
		_ = client.Disconnect(discCtx)
		srv.Close()
	})

	return srv, client
}

func (s *mockMongoServer) Commands() []recordedMongoCommand {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedMongoCommand, len(s.commands))
	copy(out, s.commands)
	return out
}

func (s *mockMongoServer) Close() {
	if s.closed.Swap(true) {
		return
	}
	_ = s.ln.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *mockMongoServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed.Load() {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conns = append(s.conns, conn)
		s.mu.Unlock()

		s.wg.Add(1)
		go s.serveConn(conn)
	}
}

func (s *mockMongoServer) serveConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	for {
		reqID, opCode, payload, err := readWireMessage(conn)
		if err != nil {
			return
		}

		switch opCode {
		case wireOpQuery:
			replyBytes, err := encodeOpReply(s.nextID.Add(1), reqID, s.helloDoc())
			if err != nil {
				return
			}
			if _, err := conn.Write(replyBytes); err != nil {
				return
			}
		case wireOpMsg:
			cmdName, cmdDoc, err := parseOpMsg(payload)
			if err != nil {
				return
			}
			var replyDoc bson.D
			switch cmdName {
			case "hello", "isMaster", "ismaster":
				replyDoc = s.helloDoc()
			case "ping", "endSessions", "killCursors":
				if cmdName == "killCursors" {
					s.record(cmdName, cmdDoc)
				}
				replyDoc = bson.D{{Key: "ok", Value: float64(1)}}
			default:
				s.record(cmdName, cmdDoc)
				if s.handler != nil {
					replyDoc = s.handler(cmdName, cmdDoc)
				}
				if replyDoc == nil {
					replyDoc = bson.D{{Key: "ok", Value: float64(1)}}
				}
			}
			replyBytes, err := encodeOpMsg(s.nextID.Add(1), reqID, replyDoc)
			if err != nil {
				return
			}
			if _, err := conn.Write(replyBytes); err != nil {
				return
			}
		default:
			return
		}
	}
}

func (s *mockMongoServer) record(name string, doc bson.M) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, recordedMongoCommand{Name: name, Doc: doc})
}

func (s *mockMongoServer) helloDoc() bson.D {
	return bson.D{
		{Key: "helloOk", Value: true},
		{Key: "ismaster", Value: true},
		{Key: "isWritablePrimary", Value: true},
		{Key: "maxBsonObjectSize", Value: int32(16777216)},
		{Key: "maxMessageSizeBytes", Value: int32(48000000)},
		{Key: "maxWriteBatchSize", Value: int32(100000)},
		{Key: "localTime", Value: time.Now()},
		{Key: "minWireVersion", Value: int32(0)},
		{Key: "maxWireVersion", Value: int32(21)},
		{Key: "ok", Value: float64(1)},
	}
}

func readWireMessage(r io.Reader) (int32, int32, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, 0, nil, err
	}
	msgLen := int32(binary.LittleEndian.Uint32(lenBuf[:]))
	if msgLen < 16 || msgLen > 48*1024*1024 {
		return 0, 0, nil, errors.New("invalid wire message length")
	}
	buf := make([]byte, msgLen)
	copy(buf[:4], lenBuf[:])
	if _, err := io.ReadFull(r, buf[4:]); err != nil {
		return 0, 0, nil, err
	}
	reqID := int32(binary.LittleEndian.Uint32(buf[4:8]))
	opCode := int32(binary.LittleEndian.Uint32(buf[12:16]))
	return reqID, opCode, buf[16:], nil
}

func parseOpMsg(payload []byte) (string, bson.M, error) {
	if len(payload) < 5 {
		return "", nil, errors.New("truncated OP_MSG")
	}
	flagBits := binary.LittleEndian.Uint32(payload[:4])
	off := 4
	end := len(payload)
	if flagBits&1 != 0 {
		end -= 4
	}

	var cmdName string
	cmdDoc := bson.M{}

	for off < end {
		kind := payload[off]
		off++
		switch kind {
		case 0:
			if off+4 > end {
				return "", nil, errors.New("truncated section 0")
			}
			docLen := int(binary.LittleEndian.Uint32(payload[off : off+4]))
			if off+docLen > end {
				return "", nil, errors.New("invalid doc length")
			}
			raw := payload[off : off+docLen]
			var ordered bson.D
			if err := bson.Unmarshal(raw, &ordered); err != nil {
				return "", nil, err
			}
			if len(ordered) > 0 && cmdName == "" {
				cmdName = ordered[0].Key
			}
			if err := bson.Unmarshal(raw, &cmdDoc); err != nil {
				return "", nil, err
			}
			off += docLen
		case 1:
			if off+4 > end {
				return "", nil, errors.New("truncated section 1")
			}
			seqSize := int(binary.LittleEndian.Uint32(payload[off : off+4]))
			seqEnd := off + seqSize
			if seqEnd > end {
				return "", nil, errors.New("invalid section 1 size")
			}
			off += 4
			nul := -1
			for i := off; i < seqEnd; i++ {
				if payload[i] == 0 {
					nul = i
					break
				}
			}
			if nul < 0 {
				return "", nil, errors.New("missing sequence identifier")
			}
			ident := string(payload[off:nul])
			off = nul + 1
			var docs []bson.M
			for off < seqEnd {
				docLen := int(binary.LittleEndian.Uint32(payload[off : off+4]))
				if off+docLen > seqEnd {
					return "", nil, errors.New("invalid sequence doc length")
				}
				var item bson.M
				if err := bson.Unmarshal(payload[off:off+docLen], &item); err != nil {
					return "", nil, err
				}
				docs = append(docs, item)
				off += docLen
			}
			cmdDoc[ident] = docs
		default:
			return "", nil, errors.New("unsupported OP_MSG section kind")
		}
	}

	return cmdName, cmdDoc, nil
}

func encodeOpReply(reqID, responseTo int32, doc bson.D) ([]byte, error) {
	body, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}
	msgLen := 16 + 20 + len(body)
	buf := make([]byte, msgLen)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(msgLen))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(reqID))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(responseTo))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(wireOpReply))
	// responseFlags=0, cursorID=0, startingFrom=0
	binary.LittleEndian.PutUint32(buf[32:36], 1) // numberReturned
	copy(buf[36:], body)
	return buf, nil
}

func encodeOpMsg(reqID, responseTo int32, doc bson.D) ([]byte, error) {
	body, err := bson.Marshal(doc)
	if err != nil {
		return nil, err
	}
	msgLen := 16 + 5 + len(body)
	buf := make([]byte, msgLen)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(msgLen))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(reqID))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(responseTo))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(wireOpMsg))
	// flagBits=0 (16..20), kind=0 (20)
	copy(buf[21:], body)
	return buf, nil
}
