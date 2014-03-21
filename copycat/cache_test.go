package copycat

import (
	"log"
	"os"
	"testing"
	"time"
)

const cacheTestLoc = "/tmp/cachetest"

func TestCache(t *testing.T) {
	// make sure we clean up after ourselves
	defer cleanUp()

	cache, err := NewCache(cacheTestLoc)
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

func cleanUp() {
	err := os.RemoveAll(cacheTestLoc)
	if err != nil {
		log.Print(err.Error())
	}
}
