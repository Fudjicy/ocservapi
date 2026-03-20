package pgwire

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

type Client struct {
	cfg  Config
	conn net.Conn
	r    *bufio.Reader
}

type Result struct {
	Columns []string
	Rows    [][]string
	Command string
}

func ParseDSN(raw string) (Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, fmt.Errorf("parse dsn: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return Config{}, fmt.Errorf("unsupported dsn scheme %q", u.Scheme)
	}
	password, _ := u.User.Password()
	cfg := Config{
		Host:     u.Hostname(),
		Port:     u.Port(),
		User:     u.User.Username(),
		Password: password,
		Database: strings.TrimPrefix(u.Path, "/"),
		SSLMode:  u.Query().Get("sslmode"),
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == "" {
		cfg.Port = "5432"
	}
	if cfg.Database == "" {
		cfg.Database = cfg.User
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}
	if cfg.SSLMode != "disable" {
		return Config{}, fmt.Errorf("only sslmode=disable is supported in sprint 1")
	}
	if cfg.User == "" {
		return Config{}, fmt.Errorf("dsn must include a username")
	}
	return cfg, nil
}

func Connect(rawDSN string) (*Client, error) {
	cfg, err := ParseDSN(rawDSN)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.Dial("tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("dial postgres: %w", err)
	}
	c := &Client{cfg: cfg, conn: conn, r: bufio.NewReader(conn)}
	if err := c.startup(); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) Ping() error {
	_, err := c.Query("SELECT 1")
	return err
}

func (c *Client) Exec(query string) error {
	_, err := c.Query(query)
	return err
}

func (c *Client) Query(query string) (Result, error) {
	if err := c.writeMessage('Q', append([]byte(query), 0)); err != nil {
		return Result{}, err
	}
	var result Result
	for {
		typ, payload, err := c.readMessage()
		if err != nil {
			return Result{}, err
		}
		switch typ {
		case 'T':
			result.Columns, err = parseRowDescription(payload)
			if err != nil {
				return Result{}, err
			}
		case 'D':
			row, err := parseDataRow(payload)
			if err != nil {
				return Result{}, err
			}
			result.Rows = append(result.Rows, row)
		case 'C':
			result.Command = readCString(payload)
		case 'I':
		case 'E':
			msg := parseError(payload)
			if err := c.drainUntilReady(); err != nil {
				return Result{}, fmt.Errorf("%s: %w", msg, err)
			}
			return Result{}, errors.New(msg)
		case 'N', 'S', 'K':
		case 'Z':
			return result, nil
		default:
			return Result{}, fmt.Errorf("unexpected postgres message %q", typ)
		}
	}
}

func (c *Client) QueryJSON(query string) ([]byte, error) {
	result, err := c.Query(query)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 || result.Rows[0][0] == "" {
		return []byte("null"), nil
	}
	return []byte(result.Rows[0][0]), nil
}

func (c *Client) startup() error {
	payload := &bytes.Buffer{}
	_ = binary.Write(payload, binary.BigEndian, int32(196608))
	writeCString(payload, "user")
	writeCString(payload, c.cfg.User)
	writeCString(payload, "database")
	writeCString(payload, c.cfg.Database)
	writeCString(payload, "client_encoding")
	writeCString(payload, "UTF8")
	payload.WriteByte(0)
	if err := c.writeRaw(payload.Bytes()); err != nil {
		return err
	}

	for {
		typ, payload, err := c.readMessage()
		if err != nil {
			return err
		}
		switch typ {
		case 'R':
			if err := c.handleAuth(payload); err != nil {
				return err
			}
		case 'S', 'K', 'N':
		case 'Z':
			return nil
		case 'E':
			return errors.New(parseError(payload))
		default:
			return fmt.Errorf("unexpected startup message %q", typ)
		}
	}
}

func (c *Client) handleAuth(payload []byte) error {
	if len(payload) < 4 {
		return fmt.Errorf("short auth payload")
	}
	code := binary.BigEndian.Uint32(payload[:4])
	rest := payload[4:]
	switch code {
	case 0:
		return nil
	case 5:
		if len(rest) != 4 {
			return fmt.Errorf("invalid md5 auth salt")
		}
		return c.sendMD5Password(rest)
	case 10:
		return c.sendSCRAMInitial(rest)
	case 11, 12:
		return fmt.Errorf("unexpected SCRAM state without negotiation")
	default:
		return fmt.Errorf("unsupported authentication method %d", code)
	}
}

func (c *Client) sendMD5Password(salt []byte) error {
	h := md5.Sum([]byte(c.cfg.Password + c.cfg.User))
	h2 := md5.Sum([]byte(hex.EncodeToString(h[:]) + string(salt)))
	password := "md5" + hex.EncodeToString(h2[:])
	return c.writeMessage('p', append([]byte(password), 0))
}

func (c *Client) sendSCRAMInitial(payload []byte) error {
	mechanisms := strings.Split(string(bytes.TrimRight(payload, "\x00")), "\x00")
	found := false
	for _, mech := range mechanisms {
		if mech == "SCRAM-SHA-256" {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("server does not offer SCRAM-SHA-256")
	}
	nonce := randomBase64(18)
	clientFirstBare := "n=" + saslName(c.cfg.User) + ",r=" + nonce
	initialResponse := "n,," + clientFirstBare
	body := &bytes.Buffer{}
	writeCString(body, "SCRAM-SHA-256")
	_ = binary.Write(body, binary.BigEndian, int32(len(initialResponse)))
	body.WriteString(initialResponse)
	if err := c.writeMessage('p', body.Bytes()); err != nil {
		return err
	}

	typ, nextPayload, err := c.readMessage()
	if err != nil {
		return err
	}
	if typ == 'E' {
		return errors.New(parseError(nextPayload))
	}
	if typ != 'R' || len(nextPayload) < 4 || binary.BigEndian.Uint32(nextPayload[:4]) != 11 {
		return fmt.Errorf("expected SASL continue")
	}
	serverFirst := strings.TrimRight(string(nextPayload[4:]), "\x00")
	values := parseSCRAMAttributes(serverFirst)
	combinedNonce := values["r"]
	salt, err := base64.StdEncoding.DecodeString(values["s"])
	if err != nil {
		return fmt.Errorf("decode scram salt: %w", err)
	}
	iterations, err := strconv.Atoi(values["i"])
	if err != nil {
		return fmt.Errorf("parse scram iterations: %w", err)
	}
	clientFinalWithoutProof := "c=biws,r=" + combinedNonce
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalWithoutProof
	saltedPassword := pbkdf2SHA256([]byte(c.cfg.Password), salt, iterations, 32)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKeySum := sha256.Sum256(clientKey)
	clientSignature := hmacSHA256(storedKeySum[:], []byte(authMessage))
	proof := xorBytes(clientKey, clientSignature)
	clientFinal := clientFinalWithoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof)
	if err := c.writeMessage('p', append([]byte(clientFinal), 0)); err != nil {
		return err
	}

	typ, nextPayload, err = c.readMessage()
	if err != nil {
		return err
	}
	if typ == 'E' {
		return errors.New(parseError(nextPayload))
	}
	if typ != 'R' || len(nextPayload) < 4 || binary.BigEndian.Uint32(nextPayload[:4]) != 12 {
		return fmt.Errorf("expected SASL final")
	}
	return nil
}

func (c *Client) drainUntilReady() error {
	for {
		typ, _, err := c.readMessage()
		if err != nil {
			return err
		}
		if typ == 'Z' {
			return nil
		}
	}
}

func (c *Client) readMessage() (byte, []byte, error) {
	typ, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint32(lenBuf[:])) - 4
	if n < 0 {
		return 0, nil, fmt.Errorf("invalid message length")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}

func (c *Client) writeRaw(payload []byte) error {
	totalLen := int32(len(payload) + 4)
	buf := &bytes.Buffer{}
	_ = binary.Write(buf, binary.BigEndian, totalLen)
	buf.Write(payload)
	_, err := c.conn.Write(buf.Bytes())
	return err
}

func (c *Client) writeMessage(typ byte, payload []byte) error {
	buf := &bytes.Buffer{}
	buf.WriteByte(typ)
	_ = binary.Write(buf, binary.BigEndian, int32(len(payload)+4))
	buf.Write(payload)
	_, err := c.conn.Write(buf.Bytes())
	return err
}

func parseRowDescription(payload []byte) ([]string, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("short row description")
	}
	count := int(binary.BigEndian.Uint16(payload[:2]))
	payload = payload[2:]
	columns := make([]string, 0, count)
	for i := 0; i < count; i++ {
		name, rest, err := splitCString(payload)
		if err != nil {
			return nil, err
		}
		if len(rest) < 18 {
			return nil, fmt.Errorf("short row description field")
		}
		columns = append(columns, name)
		payload = rest[18:]
	}
	return columns, nil
}

func parseDataRow(payload []byte) ([]string, error) {
	if len(payload) < 2 {
		return nil, fmt.Errorf("short data row")
	}
	count := int(binary.BigEndian.Uint16(payload[:2]))
	payload = payload[2:]
	row := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if len(payload) < 4 {
			return nil, fmt.Errorf("short data row column")
		}
		n := int(int32(binary.BigEndian.Uint32(payload[:4])))
		payload = payload[4:]
		if n == -1 {
			row = append(row, "")
			continue
		}
		if len(payload) < n {
			return nil, fmt.Errorf("short data row value")
		}
		row = append(row, string(payload[:n]))
		payload = payload[n:]
	}
	return row, nil
}

func parseError(payload []byte) string {
	values := map[byte]string{}
	for len(payload) > 1 {
		code := payload[0]
		if code == 0 {
			break
		}
		value, rest, err := splitCString(payload[1:])
		if err != nil {
			break
		}
		values[code] = value
		payload = rest
	}
	if msg := values['M']; msg != "" {
		return msg
	}
	return "postgres error"
}

func splitCString(payload []byte) (string, []byte, error) {
	idx := bytes.IndexByte(payload, 0)
	if idx < 0 {
		return "", nil, fmt.Errorf("unterminated cstring")
	}
	return string(payload[:idx]), payload[idx+1:], nil
}

func readCString(payload []byte) string {
	idx := bytes.IndexByte(payload, 0)
	if idx < 0 {
		return string(payload)
	}
	return string(payload[:idx])
}

func writeCString(buf *bytes.Buffer, value string) {
	buf.WriteString(value)
	buf.WriteByte(0)
}

func saslName(v string) string {
	replacer := strings.NewReplacer("=", "=3D", ",", "=2C")
	return replacer.Replace(v)
}

func parseSCRAMAttributes(input string) map[string]string {
	parts := strings.Split(input, ",")
	out := make(map[string]string, len(parts))
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			out[kv[0]] = kv[1]
		}
	}
	return out
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func xorBytes(a, b []byte) []byte {
	out := make([]byte, len(a))
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	hLen := 32
	numBlocks := (keyLen + hLen - 1) / hLen
	var out []byte
	for block := 1; block <= numBlocks; block++ {
		b := make([]byte, len(salt)+4)
		copy(b, salt)
		binary.BigEndian.PutUint32(b[len(salt):], uint32(block))
		u := hmacSHA256(password, b)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iterations; i++ {
			u = hmacSHA256(password, u)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func randomBase64(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.StdEncoding.EncodeToString(buf)
}
