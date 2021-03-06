package manta

var huf HuffmanTree

func init() {
	if huf == nil {
		huf = newFieldpathHuffman()
	}
}

// Properties is an instance of a set of properties containing key-value data.
type Properties struct {
	KV map[string]interface{}
}

// Creates a new instance of Properties.
func NewProperties() *Properties {
	return &Properties{
		KV: map[string]interface{}{},
	}
}

// Merge another set of Properties into an existing instance. Values from the
// other (merging) set overwrite those in the existing instance.
func (p *Properties) Merge(p2 *Properties) {
	for k, v := range p2.KV {
		p.KV[k] = v
	}
}

// Fetch a value by key.
func (p *Properties) Fetch(k string) (interface{}, bool) {
	v, ok := p.KV[k]
	return v, ok
}

// Fetch a bool by key.
func (p *Properties) FetchBool(k string) (bool, bool) {
	if v, ok := p.KV[k]; ok {
		if x, ok := v.(bool); ok {
			return x, true
		}
	}
	return false, false
}

// Fetch an int32 by key.
func (p *Properties) FetchInt32(k string) (int32, bool) {
	if v, ok := p.KV[k]; ok {
		if x, ok := v.(int32); ok {
			return x, true
		}
	}
	return 0, false
}

// Fetch a uint32 by key.
func (p *Properties) FetchUint32(k string) (uint32, bool) {
	if v, ok := p.KV[k]; ok {
		if x, ok := v.(uint32); ok {
			return x, true
		}
	}
	return 0, false
}

// Fetch a uint64 by key.
func (p *Properties) FetchUint64(k string) (uint64, bool) {
	if v, ok := p.KV[k]; ok {
		if x, ok := v.(uint64); ok {
			return x, true
		}
	}
	return 0, false
}

// Fetch a float32 by key.
func (p *Properties) FetchFloat32(k string) (float32, bool) {
	if v, ok := p.KV[k]; ok {
		if x, ok := v.(float32); ok {
			return x, true
		}
	}
	return 0.0, false
}

// Fetch a string by key.
func (p *Properties) FetchString(k string) (string, bool) {
	if v, ok := p.KV[k]; ok {
		if x, ok := v.(string); ok {
			return x, true
		}
	}
	return "", false
}

// Reads properties using a given reader and serializer.
func ReadProperties(r *Reader, ser *dt) (result *Properties) {
	// Return type
	result = NewProperties()

	// Create fieldpath
	fieldPath := newFieldpath(ser, &huf)

	// Get a list of the included fields
	fieldPath.walk(r)

	// iterate all the fields and set their corresponding values
	for _, f := range fieldPath.fields {
		_debugfl(6, "Decoding field %d %s %s %s", r.pos, f.Name, f.Field.Type, f.Field.Encoder)
		// r.dumpBits(1)

		if f.Field.Serializer.DecodeContainer != nil {
			_debugfl(6, "Decoding container %v", f.Field.Name)
			result.KV[f.Name] = f.Field.Serializer.DecodeContainer(r, f.Field)
		} else if f.Field.Serializer.Decode == nil {
			result.KV[f.Name] = r.readVarUint32()
			_debugfl(6, "Decoded default: %d %s %s %v", r.pos, f.Name, f.Field.Type, result.KV[f.Name])
			continue
		} else {
			result.KV[f.Name] = f.Field.Serializer.Decode(r, f.Field)
		}

		_debugfl(6, "Decoded: %d %s %s %v", r.pos, f.Name, f.Field.Type, result.KV[f.Name])
	}

	return result
}
