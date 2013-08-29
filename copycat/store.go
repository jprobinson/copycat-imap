package copycat

import (
	"bytes"
	"encoding/gob"
	"log"
	"net/mail"
	"sync"
	"time"

	"code.google.com/p/go-imap/go1/imap"
	"github.com/bradfitz/gomemcache/memcache"
)

// SearchAndStore will check check if each message in the source inbox
// exists in the destinations. If it doesn't exist in a destination, the message info will
// be pulled and stored into the destination.
func SearchAndStore(src []*imap.Client, dsts map[string][]*imap.Client) (err error) {
	var cmd *imap.Command
	cmd, err = GetAllMessages(src[0])
	if err != nil {
		log.Printf("Unable to get all messages!")
		return
	}

	// setup message fetchers to pull from the source/memcache
	var fetchers sync.WaitGroup
	fetchRequests := make(chan fetchRequest)
	for _, srcConn := range src {
		fetchers.Add(1)
		go fetchEmails(srcConn, fetchRequests, &fetchers)
	}

	// setup storers for each destination
	var storers sync.WaitGroup
	var dstsStoreRequests []chan WorkRequest
	for _, dst := range dsts {
		storeRequests := make(chan WorkRequest)
		for _, dstConn := range dst {
			storers.Add(1)
			go checkAndStoreMessages(dstConn, storeRequests, fetchRequests, &storers)
		}

		dstsStoreRequests = append(dstsStoreRequests, storeRequests)
	}

	// build the requests and send them
	log.Printf("Beginning store processing for %d messages from the source inbox", len(cmd.Data))
	var rsp *imap.Response
	var indx int
	startTime := time.Now()
	for indx, rsp = range cmd.Data {
		header := imap.AsBytes(rsp.MessageInfo().Attrs["RFC822.HEADER"])
		if msg, _ := mail.ReadMessage(bytes.NewReader(header)); msg != nil {
			header := "Message-Id"
			value := msg.Header.Get(header)

			// create the store request and pass it to each dst's storers
			storeRequest := WorkRequest{Value: value, Header: header, UID: rsp.MessageInfo().UID}
			for _, storeRequests := range dstsStoreRequests {
				storeRequests <- storeRequest
			}

			if ((indx % 100) == 0) && (indx > 0) {
				since := time.Since(startTime)
				rate := 100 / since.Seconds()
				startTime = time.Now()
				log.Printf("Completed store processing for %d messages from the source inbox. Rate: %f msg/s", indx, rate)
			}
		}
	}

	// after everything is on the channel, close them...
	for _, storeRequests := range dstsStoreRequests {
		close(storeRequests)
	}
	// ... and wait for our workers to finish up.
	storers.Wait()

	// once the storers are complete we can close the fetch channel
	close(fetchRequests)
	// and then wait for the fetchers to stop
	fetchers.Wait()

	log.Printf("search and store processes complete")
	return nil
}

func checkAndStoreMessages(dstConn *imap.Client, storeRequests chan WorkRequest, fetchRequests chan fetchRequest, wg *sync.WaitGroup) {
	defer wg.Done()

	failures := 0
	for request := range storeRequests {
	retry:
		if failures > 5 {
			log.Printf("storer encountered too many failures. giving up.")
			return
		}

		// search for in dst
		cmd, err := imap.Wait(dstConn.UIDSearch([]imap.Field{"HEADER", request.Header, request.Value}))
		if err != nil {
			log.Printf("Unable to search for message (%s): %s", request.Value, err.Error())
			// close and select.
			if err := ResetConnection(dstConn, true); err != nil {
				return
			}
			failures++
			goto retry
		}

		results := cmd.Data[0].SearchResults()
		// if not found, PULL from SRC and STORE in DST
		if len(results) == 0 {

			// build and send fetch request
			response := make(chan MessageData)
			fr := fetchRequest{MessageId: request.Value, UID: request.UID, Response: response}
			fetchRequests <- fr

			// grab response from fetchers
			messageData := <-response
			if len(messageData.Body) == 0 {
				log.Printf("No data found in message fetch request (%s)", request.Value)
				goto retry
			}

			err = AppendMessage(dstConn, messageData)
			if err != nil {
				log.Printf("Problems appending message to dst: %s", err.Error())
				if err := ResetConnection(dstConn, true); err != nil {
					return
				}
				failures++
				goto retry
			}

		}
	}
	log.Print("storer complete!")
	return
}

type fetchRequest struct {
	MessageId string
	UID       uint32
	Response  chan MessageData
}

// fetchEmails will sit and wait for fetchRequests from the destination workers. Once the
// requests channel is closed, this will finish up work and notify the waitgroup it is done.
func fetchEmails(conn *imap.Client, requests chan fetchRequest, wg *sync.WaitGroup) {
	defer wg.Done()
	// connect to memcached
	cache := memcache.New(MemcacheServer)

	failures := 0
	for request := range requests {
	retry:
		if failures > 5 {
			log.Printf("storer encountered too many failures. giving up.")
			return
		}
		
		// check if the message body is in memcached
		if msgBytes, err := cache.Get(request.MessageId); err == nil {
			var data MessageData
			err := deserialize(msgBytes.Value, &data)
			if err != nil {
				log.Printf("Problems deserializing memcache value: %s. Pulling message from src", err.Error())
				data = MessageData{}
			}

			// if its there, respond with it
			if len(data.Body) > 0 {
				request.Response <- data
				continue
			}
		}

		msgData, err := FetchMessage(conn, request.UID)
		if err != nil {
			log.Printf("Problems fetching message (%s) data: %s", request.MessageId, err.Error())
			if err := ResetConnection(conn, true); err != nil {
				return
			}
			failures++
			goto retry
		}
		request.Response <- msgData

		// store it in memcached if we had to fetch it
		// gobify
		msgGob, err := serialize(msgData)
		if err != nil {
			log.Printf("Unable to serialize message (%s): %s", request.MessageId, err.Error())
			continue
		}

		cacheItem := memcache.Item{Key: request.MessageId, Value: msgGob}
		err = cache.Add(&cacheItem)
		if err != nil {
			log.Printf("Unable to add message (%s) to cache: %s", request.MessageId, err.Error())
		}
	}
}

// Serialize encodes a value using gob.
func serialize(src interface{}) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(src)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Deserialize decodes a value using gob.
func deserialize(src []byte, dst interface{}) error {
	buf := bytes.NewBuffer(src)
	dec := gob.NewDecoder(buf)
	err := dec.Decode(dst)
	if err != nil {
		return err
	}
	return nil
}
