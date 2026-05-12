package geoip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/netip"
)

// mmdbMarker precedes the metadata block of a MaxMind DB file.
var mmdbMarker = []byte("\xAB\xCD\xEFMaxMind.com")

// ErrNotFound is returned when an address has no record.
var ErrNotFound = errors.New("address not found in database")

// Database is a read-only MaxMind DB (version 2) reader. It is safe for
// concurrent use because it never mutates its buffer.
type Database struct {
	buf        []byte
	tree       []byte
	data       []byte
	nodeCount  uint32
	recordBits uint32
	ipVersion  uint32
	ipv4Start  uint32
	treeBytes  uint32
}

// OpenDatabase parses an in-memory MaxMind DB image.
func OpenDatabase(buf []byte) (*Database, error) {
	i := bytes.LastIndex(buf, mmdbMarker)
	if i < 0 {
		return nil, errors.New("geoip database: metadata marker not found")
	}
	metaStart := i + len(mmdbMarker)
	dec := &decoder{buf: buf[metaStart:], base: 0}
	v, _, err := dec.decode(0, 0)
	if err != nil {
		return nil, fmt.Errorf("geoip database: bad metadata: %w", err)
	}
	meta, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("geoip database: metadata is not a map")
	}
	num := func(k string) (uint32, bool) {
		switch x := meta[k].(type) {
		case uint64:
			return uint32(x), true
		case int64:
			return uint32(x), true
		}
		return 0, false
	}
	nodes, ok1 := num("node_count")
	rec, ok2 := num("record_size")
	ipv, ok3 := num("ip_version")
	if !ok1 || !ok2 || !ok3 || (rec != 24 && rec != 28 && rec != 32) || (ipv != 4 && ipv != 6) {
		return nil, errors.New("geoip database: unsupported or incomplete metadata")
	}
	treeBytes := uint64(nodes) * uint64(rec) / 4
	if treeBytes+16 > uint64(i) {
		return nil, errors.New("geoip database: truncated search tree")
	}
	db := &Database{
		buf: buf, nodeCount: nodes, recordBits: rec, ipVersion: ipv, treeBytes: uint32(treeBytes),
		tree: buf[:treeBytes], data: buf[treeBytes+16 : i],
	}
	if ipv == 6 {
		node := uint32(0)
		for k := 0; k < 96 && node < nodes; k++ {
			node, err = db.record(node, 0)
			if err != nil {
				return nil, err
			}
		}
		db.ipv4Start = node
	}
	return db, nil
}

func (d *Database) record(node uint32, bit int) (uint32, error) {
	if node >= d.nodeCount {
		return 0, errors.New("geoip database: node out of range")
	}
	switch d.recordBits {
	case 24:
		o := node * 6
		b := d.tree[o : o+6]
		if bit == 0 {
			return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]), nil
		}
		return uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5]), nil
	case 28:
		o := node * 7
		b := d.tree[o : o+7]
		if bit == 0 {
			return uint32(b[3]&0xF0)<<20 | uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]), nil
		}
		return uint32(b[3]&0x0F)<<24 | uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6]), nil
	default:
		o := node * 8
		b := d.tree[o : o+8]
		if bit == 0 {
			return binary.BigEndian.Uint32(b[0:4]), nil
		}
		return binary.BigEndian.Uint32(b[4:8]), nil
	}
}

// Lookup returns the decoded record for ip.
func (d *Database) Lookup(ip netip.Addr) (any, error) {
	ip = ip.Unmap()
	var bits []byte
	node := uint32(0)
	switch {
	case ip.Is4():
		a := ip.As4()
		bits = a[:]
		if d.ipVersion == 6 {
			node = d.ipv4Start
		}
	case d.ipVersion == 6:
		a := ip.As16()
		bits = a[:]
	default:
		return nil, ErrNotFound
	}
	var err error
	for i := 0; i < len(bits)*8 && node < d.nodeCount; i++ {
		bit := int(bits[i/8]>>(7-uint(i%8))) & 1
		node, err = d.record(node, bit)
		if err != nil {
			return nil, err
		}
	}
	switch {
	case node == d.nodeCount:
		return nil, ErrNotFound
	case node < d.nodeCount:
		return nil, errors.New("geoip database: search ended inside the tree")
	}
	off := uint64(node-d.nodeCount) - 16
	if off >= uint64(len(d.data)) {
		return nil, errors.New("geoip database: data pointer out of range")
	}
	dec := &decoder{buf: d.data, base: 0}
	v, _, err := dec.decode(uint32(off), 0)
	return v, err
}

// ---- data section decoder -------------------------------------------------

type decoder struct {
	buf  []byte
	base uint32
}

const maxDecodeDepth = 32

func (d *decoder) need(off uint32, n uint32) error {
	if uint64(off)+uint64(n) > uint64(len(d.buf)) {
		return errors.New("unexpected end of data")
	}
	return nil
}

// decode reads one value at off and returns it with the offset after it.
func (d *decoder) decode(off uint32, depth int) (any, uint32, error) {
	if depth > maxDecodeDepth {
		return nil, 0, errors.New("data nested too deeply")
	}
	if err := d.need(off, 1); err != nil {
		return nil, 0, err
	}
	ctrl := d.buf[off]
	off++
	typ := ctrl >> 5
	if typ == 1 { // pointer
		ss := (ctrl >> 3) & 3
		vvv := uint32(ctrl & 7)
		n := uint32(ss) + 1
		if err := d.need(off, n); err != nil {
			return nil, 0, err
		}
		var p uint32
		switch ss {
		case 0:
			p = vvv<<8 | uint32(d.buf[off])
		case 1:
			p = (vvv<<16 | uint32(d.buf[off])<<8 | uint32(d.buf[off+1])) + 2048
		case 2:
			p = (vvv<<24 | uint32(d.buf[off])<<16 | uint32(d.buf[off+1])<<8 | uint32(d.buf[off+2])) + 526336
		default:
			p = binary.BigEndian.Uint32(d.buf[off : off+4])
		}
		v, _, err := d.decode(p, depth+1)
		return v, off + n, err
	}
	if typ == 0 { // extended type
		if err := d.need(off, 1); err != nil {
			return nil, 0, err
		}
		typ = 7 + d.buf[off]
		off++
	}
	size := uint32(ctrl & 0x1F)
	if typ != 14 { // booleans keep their value in the size bits
		switch {
		case size == 29:
			if err := d.need(off, 1); err != nil {
				return nil, 0, err
			}
			size = 29 + uint32(d.buf[off])
			off++
		case size == 30:
			if err := d.need(off, 2); err != nil {
				return nil, 0, err
			}
			size = 285 + uint32(d.buf[off])<<8 + uint32(d.buf[off+1])
			off += 2
		case size == 31:
			if err := d.need(off, 3); err != nil {
				return nil, 0, err
			}
			size = 65821 + uint32(d.buf[off])<<16 + uint32(d.buf[off+1])<<8 + uint32(d.buf[off+2])
			off += 3
		}
	}
	switch typ {
	case 2: // utf-8 string
		if err := d.need(off, size); err != nil {
			return nil, 0, err
		}
		return string(d.buf[off : off+size]), off + size, nil
	case 3: // double
		if size != 8 {
			return nil, 0, errors.New("bad double size")
		}
		if err := d.need(off, 8); err != nil {
			return nil, 0, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(d.buf[off : off+8])), off + 8, nil
	case 15: // float
		if size != 4 {
			return nil, 0, errors.New("bad float size")
		}
		if err := d.need(off, 4); err != nil {
			return nil, 0, err
		}
		return float64(math.Float32frombits(binary.BigEndian.Uint32(d.buf[off : off+4]))), off + 4, nil
	case 4: // bytes
		if err := d.need(off, size); err != nil {
			return nil, 0, err
		}
		return append([]byte(nil), d.buf[off:off+size]...), off + size, nil
	case 5, 6, 9, 10: // unsigned integers
		if size > 8 {
			return nil, 0, errors.New("integer too wide")
		}
		if err := d.need(off, size); err != nil {
			return nil, 0, err
		}
		var v uint64
		for _, b := range d.buf[off : off+size] {
			v = v<<8 | uint64(b)
		}
		return v, off + size, nil
	case 8: // int32
		if size > 4 {
			return nil, 0, errors.New("integer too wide")
		}
		if err := d.need(off, size); err != nil {
			return nil, 0, err
		}
		var v uint32
		for _, b := range d.buf[off : off+size] {
			v = v<<8 | uint32(b)
		}
		return int64(int32(v)), off + size, nil
	case 14: // boolean
		return size != 0, off, nil
	case 7: // map
		m := make(map[string]any, size)
		for i := uint32(0); i < size; i++ {
			k, next, err := d.decode(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, 0, errors.New("map key is not a string")
			}
			v, next2, err := d.decode(next, depth+1)
			if err != nil {
				return nil, 0, err
			}
			m[ks] = v
			off = next2
		}
		return m, off, nil
	case 11: // array
		if size > uint32(len(d.buf)) {
			return nil, 0, errors.New("array too large")
		}
		arr := make([]any, 0, size)
		for i := uint32(0); i < size; i++ {
			v, next, err := d.decode(off, depth+1)
			if err != nil {
				return nil, 0, err
			}
			arr = append(arr, v)
			off = next
		}
		return arr, off, nil
	case 12, 13: // cache container, end marker
		return nil, off, nil
	}
	return nil, 0, fmt.Errorf("unknown data type %d", typ)
}
