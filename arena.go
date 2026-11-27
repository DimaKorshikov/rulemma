package rulemma

import (
	"bytes"
	"hash/maphash"
	"strings"
	"unsafe"
)

// Hardware-accelerated hash seed (runtime AES-NI on x86_64/arm64, zero allocations).
var arenaHashSeed = maphash.MakeSeed()

func hashBytes(b []byte) uint64 {
	return maphash.Bytes(arenaHashSeed, b)
}

func hashString(s string) uint64 {
	return maphash.String(arenaHashSeed, s)
}

// Lemire fast-range reduction: maps a uniform 32-bit hash into [0, n) with one 64-bit multiply.
func fastRange(h uint32, n uint32) uint32 {
	return uint32((uint64(h) * uint64(n)) >> 32)
}

// isFastLowerNoYo returns true if b contains only lowercase ASCII / digits / punctuation
// and lowercase Russian Cyrillic (а..я) without ё/Ё or uppercase letters.
// When true, b needs neither strings.ToLower nor normYo.
func isFastLowerNoYo(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			if c >= 'A' && c <= 'Z' {
				return false
			}
			i++
			continue
		}
		if i+1 >= len(b) {
			return false
		}
		c2 := b[i+1]
		if c == 0xD0 {
			// Lowercase Cyrillic а..п are 0xD0 0xB0..0xBF.
			if c2 < 0xB0 || c2 > 0xBF {
				return false
			}
			i += 2
			continue
		}
		if c == 0xD1 {
			// Lowercase Cyrillic р..я are 0xD1 0x80..0x8F (ё is 0xD1 0x91 -> rejected).
			if c2 < 0x80 || c2 > 0x8F {
				return false
			}
			i += 2
			continue
		}
		return false
	}
	return true
}

func normalizeLowerNoYoBytes(b []byte, scratch []byte) []byte {
	if isFastLowerNoYo(b) {
		return b
	}
	s := normYo(strings.ToLower(string(b)))
	return append(scratch[:0], s...)
}

func normalizeLowerBytes(b []byte, scratch []byte) []byte {
	// Checks ASCII uppercase and Cyrillic uppercase without allocating on already-lowercase input.
	allLower := true
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			if c >= 'A' && c <= 'Z' {
				allLower = false
				break
			}
			i++
			continue
		}
		if i+1 >= len(b) {
			allLower = false
			break
		}
		c2 := b[i+1]
		if c == 0xD0 && c2 >= 0xB0 && c2 <= 0xBF {
			i += 2
			continue
		}
		if c == 0xD1 && ((c2 >= 0x80 && c2 <= 0x8F) || c2 == 0x91) {
			i += 2
			continue
		}
		allLower = false
		break
	}
	if allLower {
		return b
	}
	return append(scratch[:0], strings.ToLower(string(b))...)
}

// --- compactStrMap: pointer-free byte arena + open-addressed hash table (string -> string) ---

type strSlot struct {
	keyOff uint32 // byte offset in arena; 0 marks an empty slot (arena[0] is a sentinel byte)
	valOff uint32 // byte offset in arena
	keyLen uint8  // byte length of key (<= 255)
	valLen uint8  // byte length of value (<= 255)
	tag    uint16 // upper 16 bits of mixed hash for cache-friendly mismatch filtering
}

type compactStrMap struct {
	arena      []byte
	arenaStr   string
	slots      []strSlot
	n          int
	lastValOff uint32
	lastValLen uint8
	overflow   map[string]string
}

func newCompactStrMap(expectedEntries int, arenaHint int) *compactStrMap {
	slotCap := expectedEntries*10/7 + 16
	if slotCap < 32 {
		slotCap = 32
	}
	if arenaHint < 64 {
		arenaHint = 64
	}
	arena := make([]byte, 1, arenaHint) // arena[0] = 0 sentinel so keyOff == 0 means empty
	return &compactStrMap{
		arena: arena,
		slots: make([]strSlot, slotCap),
	}
}

func (m *compactStrMap) Len() int {
	if m == nil {
		return 0
	}
	return m.n + len(m.overflow)
}

func (m *compactStrMap) findKeySpanBytes(key []byte) (uint32, uint8, bool) {
	if len(key) == 0 || len(key) > 255 || len(m.slots) == 0 {
		return 0, 0, false
	}
	h := hashBytes(key)
	tag := uint16(h >> 48)
	nSlots := uint32(len(m.slots))
	idx := fastRange(uint32(h), nSlots)
	for {
		s := m.slots[idx]
		if s.keyOff == 0 {
			return 0, 0, false
		}
		if s.tag == tag && int(s.keyLen) == len(key) && bytes.Equal(m.arena[s.keyOff:s.keyOff+uint32(s.keyLen)], key) {
			return s.keyOff, s.keyLen, true
		}
		idx++
		if idx == nSlots {
			idx = 0
		}
	}
}

func (m *compactStrMap) rehash(newCap int) {
	oldSlots := m.slots
	newSlots := make([]strSlot, newCap)
	nSlots := uint32(newCap)
	for i := range oldSlots {
		s := oldSlots[i]
		if s.keyOff == 0 {
			continue
		}
		k := m.arena[s.keyOff : s.keyOff+uint32(s.keyLen)]
		h := hashBytes(k)
		idx := fastRange(uint32(h), nSlots)
		for newSlots[idx].keyOff != 0 {
			idx++
			if idx == nSlots {
				idx = 0
			}
		}
		newSlots[idx] = s
	}
	m.slots = newSlots
}

func (m *compactStrMap) appendArena(b []byte) (uint32, uint8) {
	off := uint32(len(m.arena))
	m.arena = append(m.arena, b...)
	return off, uint8(len(b))
}

func (m *compactStrMap) putBytes(key, val []byte, internVal bool) {
	if len(key) == 0 {
		return
	}
	if len(key) > 255 || len(val) > 255 {
		if m.overflow == nil {
			m.overflow = make(map[string]string, 4)
		}
		m.overflow[string(key)] = string(val)
		return
	}
	if (m.n+1)*10 >= len(m.slots)*8 {
		m.rehash(len(m.slots)*3/2 + 16)
	}
	h := hashBytes(key)
	tag := uint16(h >> 48)
	nSlots := uint32(len(m.slots))
	idx := fastRange(uint32(h), nSlots)
	for {
		s := &m.slots[idx]
		if s.keyOff == 0 {
			kOff, kLen := m.appendArena(key)
			var vOff uint32
			var vLen uint8
			if bytes.Equal(key, val) {
				vOff, vLen = kOff, kLen
			} else if internVal && m.lastValOff != 0 && int(m.lastValLen) == len(val) &&
				bytes.Equal(m.arena[m.lastValOff:m.lastValOff+uint32(m.lastValLen)], val) {
				vOff, vLen = m.lastValOff, m.lastValLen
			} else if internVal {
				if eOff, eLen, ok := m.findKeySpanBytes(val); ok {
					vOff, vLen = eOff, eLen
				} else {
					vOff, vLen = m.appendArena(val)
				}
			} else {
				vOff, vLen = m.appendArena(val)
			}
			if internVal {
				m.lastValOff, m.lastValLen = vOff, vLen
			}
			*s = strSlot{
				keyOff: kOff,
				valOff: vOff,
				keyLen: kLen,
				valLen: vLen,
				tag:    tag,
			}
			m.n++
			return
		}
		if s.tag == tag && int(s.keyLen) == len(key) && bytes.Equal(m.arena[s.keyOff:s.keyOff+uint32(s.keyLen)], key) {
			if bytes.Equal(key, val) {
				s.valOff, s.valLen = s.keyOff, s.keyLen
			} else if int(s.valLen) == len(val) && bytes.Equal(m.arena[s.valOff:s.valOff+uint32(s.valLen)], val) {
				// unchanged
			} else {
				s.valOff, s.valLen = m.appendArena(val)
			}
			return
		}
		idx++
		if idx == nSlots {
			idx = 0
		}
	}
}

func (m *compactStrMap) putString(key, val string, internVal bool) {
	m.putBytes([]byte(key), []byte(val), internVal)
}

// freeze seals the arena for zero-allocation concurrent reads.
func (m *compactStrMap) freeze() {
	if m == nil {
		return
	}
	// Trim excess arena capacity if significantly over-allocated (>256 KB slack).
	if cap(m.arena)-len(m.arena) > 256*1024 {
		trimmed := make([]byte, len(m.arena))
		copy(trimmed, m.arena)
		m.arena = trimmed
	}
	if len(m.arena) > 0 {
		m.arenaStr = unsafe.String(unsafe.SliceData(m.arena), len(m.arena))
	}
}

func (m *compactStrMap) Get(key string) (string, bool) {
	if m == nil || (m.n == 0 && len(m.overflow) == 0) {
		return "", false
	}
	if len(key) > 255 {
		if m.overflow != nil {
			v, ok := m.overflow[key]
			return v, ok
		}
		return "", false
	}
	if m.n == 0 {
		return "", false
	}
	h := hashString(key)
	tag := uint16(h >> 48)
	nSlots := uint32(len(m.slots))
	idx := fastRange(uint32(h), nSlots)
	arenaStr := m.arenaStr
	for {
		s := m.slots[idx]
		if s.keyOff == 0 {
			return "", false
		}
		if s.tag == tag && int(s.keyLen) == len(key) && arenaStr[s.keyOff:s.keyOff+uint32(s.keyLen)] == key {
			return arenaStr[s.valOff : s.valOff+uint32(s.valLen)], true
		}
		idx++
		if idx == nSlots {
			idx = 0
		}
	}
}

// --- compactU32Map: pointer-free byte arena + open-addressed hash table (string -> uint32) ---

type u32Slot struct {
	keyOff uint32 // byte offset in arena; 0 marks an empty slot
	val    uint32
	keyLen uint8
	_      uint8
	tag    uint16
}

type compactU32Map struct {
	arena    []byte
	arenaStr string
	slots    []u32Slot
	n        int
	overflow map[string]uint32
}

func newCompactU32Map(expectedEntries int, arenaHint int) *compactU32Map {
	slotCap := expectedEntries*10/7 + 16
	if slotCap < 32 {
		slotCap = 32
	}
	if arenaHint < 64 {
		arenaHint = 64
	}
	return &compactU32Map{
		arena: make([]byte, 1, arenaHint),
		slots: make([]u32Slot, slotCap),
	}
}

func (m *compactU32Map) Len() int {
	if m == nil {
		return 0
	}
	return m.n + len(m.overflow)
}

func (m *compactU32Map) rehash(newCap int) {
	oldSlots := m.slots
	newSlots := make([]u32Slot, newCap)
	nSlots := uint32(newCap)
	for i := range oldSlots {
		s := oldSlots[i]
		if s.keyOff == 0 {
			continue
		}
		k := m.arena[s.keyOff : s.keyOff+uint32(s.keyLen)]
		h := hashBytes(k)
		idx := fastRange(uint32(h), nSlots)
		for newSlots[idx].keyOff != 0 {
			idx++
			if idx == nSlots {
				idx = 0
			}
		}
		newSlots[idx] = s
	}
	m.slots = newSlots
}

func (m *compactU32Map) putBytes(key []byte, val uint32) {
	if len(key) == 0 {
		return
	}
	if len(key) > 255 {
		if m.overflow == nil {
			m.overflow = make(map[string]uint32, 4)
		}
		m.overflow[string(key)] = val
		return
	}
	if (m.n+1)*10 >= len(m.slots)*8 {
		m.rehash(len(m.slots)*3/2 + 16)
	}
	h := hashBytes(key)
	tag := uint16(h >> 48)
	nSlots := uint32(len(m.slots))
	idx := fastRange(uint32(h), nSlots)
	for {
		s := &m.slots[idx]
		if s.keyOff == 0 {
			off := uint32(len(m.arena))
			m.arena = append(m.arena, key...)
			*s = u32Slot{
				keyOff: off,
				val:    val,
				keyLen: uint8(len(key)),
				tag:    tag,
			}
			m.n++
			return
		}
		if s.tag == tag && int(s.keyLen) == len(key) && bytes.Equal(m.arena[s.keyOff:s.keyOff+uint32(s.keyLen)], key) {
			s.val = val
			return
		}
		idx++
		if idx == nSlots {
			idx = 0
		}
	}
}

func (m *compactU32Map) freeze() {
	if m == nil {
		return
	}
	if cap(m.arena)-len(m.arena) > 256*1024 {
		trimmed := make([]byte, len(m.arena))
		copy(trimmed, m.arena)
		m.arena = trimmed
	}
	if len(m.arena) > 0 {
		m.arenaStr = unsafe.String(unsafe.SliceData(m.arena), len(m.arena))
	}
}

func (m *compactU32Map) Get(key string) (uint32, bool) {
	if m == nil || (m.n == 0 && len(m.overflow) == 0) {
		return 0, false
	}
	if len(key) > 255 {
		if m.overflow != nil {
			v, ok := m.overflow[key]
			return v, ok
		}
		return 0, false
	}
	if m.n == 0 {
		return 0, false
	}
	h := hashString(key)
	tag := uint16(h >> 48)
	nSlots := uint32(len(m.slots))
	idx := fastRange(uint32(h), nSlots)
	arenaStr := m.arenaStr
	for {
		s := m.slots[idx]
		if s.keyOff == 0 {
			return 0, false
		}
		if s.tag == tag && int(s.keyLen) == len(key) && arenaStr[s.keyOff:s.keyOff+uint32(s.keyLen)] == key {
			return s.val, true
		}
		idx++
		if idx == nSlots {
			idx = 0
		}
	}
}

// --- adjFormsTable: compact 4-slot agreement table keyed by adjective base (0 GC pointers!) ---
// Standard adj_forms keys have 4 forms per base:
//   slot 0: masc|sing
//   slot 1: femn|sing
//   slot 2: neut|sing
//   slot 3: |plur (gender == "" && number == "plur")
// Any non-standard gender|number combination falls back cleanly to extra.

type adjSlot struct {
	baseOff uint32
	valOff  [4]uint32
	valLen  [4]uint8
	baseLen uint8
	_       uint8
	tag     uint16
}

type adjFormsTable struct {
	arena    []byte
	arenaStr string
	slots    []adjSlot
	bases    int
	entries  int
	extra    map[string]string
}

func newAdjFormsTable(expectedBases int, arenaHint int) *adjFormsTable {
	slotCap := expectedBases*10/7 + 16
	if slotCap < 32 {
		slotCap = 32
	}
	if arenaHint < 64 {
		arenaHint = 64
	}
	return &adjFormsTable{
		arena: make([]byte, 1, arenaHint),
		slots: make([]adjSlot, slotCap),
	}
}

func (t *adjFormsTable) Len() int {
	if t == nil {
		return 0
	}
	return t.entries
}

func adjSlotIndexBytes(gn []byte) int {
	switch string(gn) {
	case "masc|sing":
		return 0
	case "femn|sing":
		return 1
	case "neut|sing":
		return 2
	case "|plur":
		return 3
	default:
		return -1
	}
}

func adjSlotIndex(gender, number string) int {
	if number == "plur" && gender == "" {
		return 3
	}
	if number == "sing" {
		switch gender {
		case "masc":
			return 0
		case "femn":
			return 1
		case "neut":
			return 2
		}
	}
	return -1
}

func (t *adjFormsTable) rehash(newCap int) {
	oldSlots := t.slots
	newSlots := make([]adjSlot, newCap)
	nSlots := uint32(newCap)
	for i := range oldSlots {
		s := oldSlots[i]
		if s.baseOff == 0 {
			continue
		}
		b := t.arena[s.baseOff : s.baseOff+uint32(s.baseLen)]
		h := hashBytes(b)
		idx := fastRange(uint32(h), nSlots)
		for newSlots[idx].baseOff != 0 {
			idx++
			if idx == nSlots {
				idx = 0
			}
		}
		newSlots[idx] = s
	}
	t.slots = newSlots
}

func (t *adjFormsTable) putComposite(fullKey, val []byte) {
	if len(fullKey) == 0 || len(val) == 0 {
		return
	}
	sep := bytes.IndexByte(fullKey, '|')
	if sep > 0 && sep <= 255 && len(val) <= 255 {
		base := fullKey[:sep]
		slotIdx := adjSlotIndexBytes(fullKey[sep+1:])
		if slotIdx >= 0 {
			if (t.bases+1)*10 >= len(t.slots)*8 {
				t.rehash(len(t.slots)*3/2 + 16)
			}
			h := hashBytes(base)
			tag := uint16(h >> 48)
			nSlots := uint32(len(t.slots))
			idx := fastRange(uint32(h), nSlots)
			for {
				s := &t.slots[idx]
				if s.baseOff == 0 {
					bOff := uint32(len(t.arena))
					t.arena = append(t.arena, base...)
					var vOff uint32
					if bytes.Equal(base, val) {
						vOff = bOff
					} else {
						vOff = uint32(len(t.arena))
						t.arena = append(t.arena, val...)
					}
					s.baseOff = bOff
					s.baseLen = uint8(len(base))
					s.tag = tag
					s.valOff[slotIdx] = vOff
					s.valLen[slotIdx] = uint8(len(val))
					t.bases++
					t.entries++
					return
				}
				if s.tag == tag && int(s.baseLen) == len(base) && bytes.Equal(t.arena[s.baseOff:s.baseOff+uint32(s.baseLen)], base) {
					wasEmpty := s.valOff[slotIdx] == 0
					if bytes.Equal(base, val) {
						s.valOff[slotIdx] = s.baseOff
						s.valLen[slotIdx] = s.baseLen
					} else {
						vOff := uint32(len(t.arena))
						t.arena = append(t.arena, val...)
						s.valOff[slotIdx] = vOff
						s.valLen[slotIdx] = uint8(len(val))
					}
					if wasEmpty {
						t.entries++
					}
					return
				}
				idx++
				if idx == nSlots {
					idx = 0
				}
			}
		}
	}
	// Non-standard key fallback
	if t.extra == nil {
		t.extra = make(map[string]string, 8)
	}
	k := string(fullKey)
	if _, exists := t.extra[k]; !exists {
		t.entries++
	}
	t.extra[k] = string(val)
}

func (t *adjFormsTable) freeze() {
	if t == nil {
		return
	}
	if cap(t.arena)-len(t.arena) > 256*1024 {
		trimmed := make([]byte, len(t.arena))
		copy(trimmed, t.arena)
		t.arena = trimmed
	}
	if len(t.arena) > 0 {
		t.arenaStr = unsafe.String(unsafe.SliceData(t.arena), len(t.arena))
	}
}

func (t *adjFormsTable) Get(base, gender, number string) (string, bool) {
	if t == nil || t.entries == 0 {
		return "", false
	}
	slotIdx := adjSlotIndex(gender, number)
	if slotIdx >= 0 && len(base) > 0 && len(base) <= 255 && t.bases > 0 {
		h := hashString(base)
		tag := uint16(h >> 48)
		nSlots := uint32(len(t.slots))
		idx := fastRange(uint32(h), nSlots)
		arenaStr := t.arenaStr
		for {
			s := t.slots[idx]
			if s.baseOff == 0 {
				return "", false
			}
			if s.tag == tag && int(s.baseLen) == len(base) && arenaStr[s.baseOff:s.baseOff+uint32(s.baseLen)] == base {
				off := s.valOff[slotIdx]
				if off == 0 {
					return "", false
				}
				return arenaStr[off : off+uint32(s.valLen[slotIdx])], true
			}
			idx++
			if idx == nSlots {
				idx = 0
			}
		}
	}
	if t.extra != nil {
		v, ok := t.extra[base+"|"+gender+"|"+number]
		return v, ok
	}
	return "", false
}
