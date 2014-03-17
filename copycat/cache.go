package copycat

import (
	"bytes"
	"encoding/gob"
	"errors"

	"github.com/syndtr/goleveldb/leveldb"
)

type Cache struct {
	db *leveldb.DB
}

func NewCache(dbPath string) (*Cache, error) {
	c := &Cache{}
	var err error
	c.db, err = leveldb.OpenFile(dbPath, nil)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Cache) Close() {
	c.db.Close()
}

// our own so we dont have to include leveldb elsewhere
var ErrNotFound = errors.New("not found")

func (c *Cache) Get(id string) (MessageData, error) {
	var md MessageData
	rawData, err := c.db.Get([]byte(id), nil)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return md, ErrNotFound
		}
		return md, err
	}
	err = deserialize(rawData, &md)
	if err != nil {
		return md, err
	}

	return md, nil
}

func (c *Cache) Put(id string, data MessageData) error {
	rawData, err := serialize(data)
	if err != nil {
		return err
	}

	return c.db.Put([]byte(id), rawData, nil)
}

// serialize encodes a value using gob.
func serialize(src interface{}) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(src)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// deserialize decodes a value using gob.
func deserialize(src []byte, dst interface{}) error {
	buf := bytes.NewBuffer(src)
	dec := gob.NewDecoder(buf)
	err := dec.Decode(dst)
	if err != nil {
		return err
	}
	return nil
}
