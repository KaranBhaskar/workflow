package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"workflow/internal/executor"
)

type RedisQueue struct {
	addr        string
	key         string
	dialTimeout time.Duration
}

func NewRedisQueue(addr, key string) *RedisQueue {
	if strings.TrimSpace(key) == "" {
		key = "workflow:jobs:async"
	}

	return &RedisQueue{
		addr:        strings.TrimSpace(addr),
		key:         key,
		dialTimeout: 2 * time.Second,
	}
}

func (q *RedisQueue) Enqueue(ctx context.Context, job executor.AsyncJob) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal async job: %w", err)
	}

	conn, reader, writer, err := q.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := writeRESPArray(writer, "LPUSH", q.key, string(payload)); err != nil {
		return err
	}
	if _, err := readRESPInteger(reader); err != nil {
		return err
	}

	return nil
}

func (q *RedisQueue) Dequeue(ctx context.Context, wait time.Duration) (executor.AsyncJob, bool, error) {
	timeoutSeconds := int(wait.Seconds())
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}

	conn, reader, writer, err := q.connect(ctx)
	if err != nil {
		return executor.AsyncJob{}, false, err
	}
	defer conn.Close()

	if err := writeRESPArray(writer, "BRPOP", q.key, strconv.Itoa(timeoutSeconds)); err != nil {
		return executor.AsyncJob{}, false, err
	}

	values, ok, err := readRESPArray(reader)
	if err != nil || !ok {
		return executor.AsyncJob{}, ok, err
	}
	if len(values) != 2 {
		return executor.AsyncJob{}, false, fmt.Errorf("unexpected BRPOP response length %d", len(values))
	}

	var job executor.AsyncJob
	if err := json.Unmarshal([]byte(values[1]), &job); err != nil {
		return executor.AsyncJob{}, false, fmt.Errorf("decode async job: %w", err)
	}

	return job, true, nil
}

func (q *RedisQueue) connect(ctx context.Context) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
	dialer := net.Dialer{Timeout: q.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", q.addr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect redis queue: %w", err)
	}

	return conn, bufio.NewReader(conn), bufio.NewWriter(conn), nil
}

func writeRESPArray(writer *bufio.Writer, values ...string) error {
	if _, err := fmt.Fprintf(writer, "*%d\r\n", len(values)); err != nil {
		return fmt.Errorf("write redis command: %w", err)
	}
	for _, value := range values {
		if _, err := fmt.Fprintf(writer, "$%d\r\n%s\r\n", len(value), value); err != nil {
			return fmt.Errorf("write redis argument: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush redis command: %w", err)
	}

	return nil
}

func readRESPInteger(reader *bufio.Reader) (int, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("read redis integer prefix: %w", err)
	}
	if prefix != ':' {
		return 0, fmt.Errorf("unexpected redis integer prefix %q", prefix)
	}

	line, err := readRESPLine(reader)
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("parse redis integer: %w", err)
	}

	return value, nil
}

func readRESPArray(reader *bufio.Reader) ([]string, bool, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, false, fmt.Errorf("read redis array prefix: %w", err)
	}
	if prefix != '*' {
		return nil, false, fmt.Errorf("unexpected redis array prefix %q", prefix)
	}

	line, err := readRESPLine(reader)
	if err != nil {
		return nil, false, err
	}
	if line == "-1" {
		return nil, false, nil
	}

	count, err := strconv.Atoi(line)
	if err != nil {
		return nil, false, fmt.Errorf("parse redis array length: %w", err)
	}

	values := make([]string, 0, count)
	for range count {
		value, ok, err := readRESPBulkString(reader)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		values = append(values, value)
	}

	return values, true, nil
}

func readRESPBulkString(reader *bufio.Reader) (string, bool, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return "", false, fmt.Errorf("read redis bulk string prefix: %w", err)
	}
	if prefix != '$' {
		return "", false, fmt.Errorf("unexpected redis bulk string prefix %q", prefix)
	}

	line, err := readRESPLine(reader)
	if err != nil {
		return "", false, err
	}
	if line == "-1" {
		return "", false, nil
	}

	size, err := strconv.Atoi(line)
	if err != nil {
		return "", false, fmt.Errorf("parse redis bulk string length: %w", err)
	}

	buffer := make([]byte, size+2)
	if _, err := reader.Read(buffer); err != nil {
		return "", false, fmt.Errorf("read redis bulk string payload: %w", err)
	}

	return string(buffer[:size]), true, nil
}

func readRESPLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read redis line: %w", err)
	}

	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}
