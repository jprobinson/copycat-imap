package copycat

import (
	"log"
	"testing"
	"time"
)

func TestCache(t *testing.T) {

	cache, err := NewCache("/tmp/cachetest")
	if err != nil {
		t.Errorf("unable to create cache - %s", err.Error())
		return
	}
	defer cache.Close()

	data := MessageData{InternalDate: time.Now(), Body: []byte("this is some data")}
	key := "key123"

	err = cache.Put(key, data)
	if err != nil {
		t.Errorf("unable to put in create  - %s", err.Error())
		return
	}

	var newData MessageData
	newData, err = cache.Get(key)
	if err != nil {
		t.Errorf("unable to put in create  - %s", err.Error())
		return
	}

	if newData.InternalDate != data.InternalDate || len(newData.Body) != len(data.Body) {
		t.Errorf("cache returned %v - expected %v", newData, data)
		return
	}

	log.Printf("cache result - %v - expected %v", newData, data)

}
