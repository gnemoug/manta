package manta

import (
	"github.com/dotabuff/manta/dota"
	"github.com/golang/snappy"
)

const (
	STRINGTABLE_KEY_HISTORY_SIZE = 32
)

// Holds and maintains the string table information for an
// instance of the Parser.
type StringTables struct {
	Tables    map[int32]*StringTable
	NameIndex map[string]int32
	nextIndex int32
}

// Retrieves a string table by its name. Check the bool.
func (ts *StringTables) GetTableByName(name string) (*StringTable, bool) {
	i, ok := ts.NameIndex[name]
	if !ok {
		return nil, false
	}
	t, ok := ts.Tables[i]
	return t, ok
}

// Creates a new empty StringTables.
func newStringTables() *StringTables {
	return &StringTables{
		Tables:    make(map[int32]*StringTable),
		NameIndex: make(map[string]int32),
		nextIndex: 0,
	}
}

// Holds and maintains the information for a string table.
type StringTable struct {
	index             int32
	name              string
	Items             map[int32]*StringTableItem
	userDataFixedSize bool
	userDataSize      int32
}

func (st *StringTable) GetIndex() int32                      { return st.index }
func (st *StringTable) GetName() string                      { return st.name }
func (st *StringTable) GetItem(index int32) *StringTableItem { return st.Items[index] }

// Holds and maintains a single entry in a string table.
type StringTableItem struct {
	Index int32
	Key   string
	Value []byte
}

// Internal callback for CDemoStringTables.
// These appear to be periodic state dumps and appear every 1800 outer ticks.
// XXX TODO: decide if we want to at all integrate these updates,
// or trust create/update entirely. Let's ignore them for now.
func (p *Parser) onCDemoStringTables(m *dota.CDemoStringTables) error {
	return nil
}

// Internal callback for CSVCMsg_CreateStringTable.
// XXX TODO: This is currently using an artificial, internally crafted message.
// This should be replaced with the real message once we have updated protos.
func (p *Parser) onCSVCMsg_CreateStringTable(m *dota.CSVCMsg_CreateStringTable) error {
	// Create a new string table at the next index position
	t := &StringTable{
		index:             p.StringTables.nextIndex,
		name:              m.GetName(),
		Items:             make(map[int32]*StringTableItem),
		userDataFixedSize: m.GetUserDataFixedSize(),
		userDataSize:      m.GetUserDataSize(),
	}

	// Increment the index
	p.StringTables.nextIndex += 1

	// Decompress the data if necessary
	buf := m.GetStringData()
	if m.GetDataCompressed() {
		// old replays = lzss
		// new replays = snappy

		r := NewReader(buf)
		var err error

		if s := r.readStringN(4); s != "LZSS" {
			if buf, err = snappy.Decode(nil, buf); err != nil {
				return err
			}
		} else {
			if buf, err = unlzss(buf); err != nil {
				return err
			}
		}
	}

	// Parse the items out of the string table data
	items := parseStringTable(buf, m.GetNumEntries(), t.userDataFixedSize, t.userDataSize)

	// Insert the items into the table
	for _, item := range items {
		t.Items[item.Index] = item
	}

	// Add the table to the parser state
	p.StringTables.Tables[t.index] = t
	p.StringTables.NameIndex[t.name] = t.index

	// Apply the updates to baseline state
	if t.name == "instancebaseline" {
		p.updateInstanceBaseline()
	}

	return nil
}

// Internal callback for CSVCMsg_UpdateStringTable.
func (p *Parser) onCSVCMsg_UpdateStringTable(m *dota.CSVCMsg_UpdateStringTable) error {
	// TODO: integrate
	t, ok := p.StringTables.Tables[m.GetTableId()]
	if !ok {
		_panicf("missing string table %d", m.GetTableId())
	}

	_tracef("tick=%d name=%s changedEntries=%d buflen=%d", p.Tick, t.name, m.GetNumChangedEntries(), len(m.GetStringData()))

	// Parse the updates out of the string table data
	items := parseStringTable(m.GetStringData(), m.GetNumChangedEntries(), t.userDataFixedSize, t.userDataSize)

	// Apply the updates to the parser state
	for _, item := range items {
		index := item.Index
		if _, ok := t.Items[index]; ok {
			// XXX TODO: Sometimes ActiveModifiers change keys, which is suspicous...
			if item.Key != "" && item.Key != t.Items[index].Key {
				_tracef("tick=%d name=%s index=%d key='%s' update key -> %s", p.Tick, t.name, index, t.Items[index].Key, item.Key)
				t.Items[index].Key = item.Key
			}
			if len(item.Value) > 0 {
				_tracef("tick=%d name=%s index=%d key='%s' update value len %d -> %d", p.Tick, t.name, index, t.Items[index].Key, len(t.Items[index].Value), len(item.Value))
				t.Items[index].Value = item.Value
			}
		} else {
			_tracef("tick=%d name=%s inserting new item %d key '%s'", p.Tick, t.name, index, item.Key)
			t.Items[index] = item
		}
	}

	// Apply the updates to baseline state
	if t.name == "instancebaseline" {
		p.updateInstanceBaseline()
	}

	return nil
}

// Parse a string table data blob, returning a list of item updates.
func parseStringTable(buf []byte, numUpdates int32, userDataFixed bool, userDataSize int32) (items []*StringTableItem) {
	items = make([]*StringTableItem, 0)

	// Create a reader for the buffer
	r := NewReader(buf)

	// Start with an index of -1.
	// If the first item is at index 0 it will use a incr operation.
	index := int32(-1)

	// Maintain a list of key history
	keys := make([]string, 0, STRINGTABLE_KEY_HISTORY_SIZE)

	// Some tables have no data
	if len(buf) == 0 {
		return items
	}

	// Loop through entries in the data structure
	//
	// Each entry is a tuple consisting of {index, key, value}
	//
	// Index can either be incremented from the previous position or
	// overwritten with a given entry.
	//
	// Key may be omitted (will be represented here as "")
	//
	// Value may be omitted
	for i := 0; i < int(numUpdates); i++ {
		key := ""
		value := []byte{}

		// Read a boolean to determine whether the operation is an increment or
		// has a fixed index position. A fixed index position of zero should be
		// the last data in the buffer, and indicates that all data has been read.
		incr := r.readBoolean()
		if incr {
			index++
		} else {
			index = int32(r.readVarUint32()) + 1
		}

		// Some values have keys, some don't.
		hasKey := r.readBoolean()
		if hasKey {
			// Some entries use reference a position in the key history for
			// part of the key. If referencing the history, read the position
			// and size from the buffer, then use those to build the string
			// combined with an extra string read (null terminated).
			// Alternatively, just read the string.
			useHistory := r.readBoolean()
			if useHistory {
				pos := r.readBits(5)
				size := r.readBits(5)

				if int(pos) >= len(keys) {
					key += r.readString()
				} else {
					s := keys[pos]
					if int(size) > len(s) {
						key += s + r.readString()
					} else {
						key += s[0:size] + r.readString()
					}
				}
			} else {
				key = r.readString()
			}

			if len(keys) >= STRINGTABLE_KEY_HISTORY_SIZE {
				copy(keys[0:], keys[1:])
				keys[len(keys)-1] = ""
				keys = keys[:len(keys)-1]
			}
			keys = append(keys, key)
		}

		// Some entries have a value.
		hasValue := r.readBoolean()
		if hasValue {
			// Values can be either fixed size (with a size specified in
			// bits during table creation, or have a variable size with
			// a 14-bit prefixed size.
			if userDataFixed {
				value = r.readBitsAsBytes(int(userDataSize))
			} else {
				size := int(r.readBits(14))
				r.readBits(3) // XXX TODO: what is this?
				value = r.readBytes(size)
			}
		}

		items = append(items, &StringTableItem{index, key, value})
	}

	return items
}
