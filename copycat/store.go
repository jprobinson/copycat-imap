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
	fetchRequests := make(chan fetchRequest)
	for _, srcConn := range src {
		go fetchEmails(srcConn, fetchRequests)
	}
	
	var appendRequests []chan WorkRequest
	var storers sync.WaitGroup
	// setup storers for each destination
	for _, dst := range dsts {
		storeRequests := make(chan WorkRequest)
		for _, dstConn := range dst {
			storers.Add(1)
			go CheckAndAppendMessages(dstConn, storeRequests, fetchRequests, &storers)
		}
		appendRequests = append(appendRequests, storeRequests)
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
			for _, storeRequests := range appendRequests {
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
	for _, storeRequests := range appendRequests {
		close(storeRequests)
	}
	// ... and wait for our workers to finish up.
	storers.Wait()

	// once the storers are complete we can close the fetch channel
	close(fetchRequests)

	log.Printf("search and store processes complete")
	return nil
}

// checkAndStoreMessages will wait for WorkRequests to come acorss the pipe. When it receives a request, it will search
// the given destination inbox for the message. If it is not found, this method will attempt to pull the messages data
// from fetchRequests and then append it to the destination.
func CheckAndAppendMessages(dstConn *imap.Client, storeRequests chan WorkRequest, fetchRequests chan fetchRequest, wg *sync.WaitGroup) {
	defer wg.Done()

	// noop it every few to keep things alive
	timeout := time.NewTicker(NoopMinutes * time.Minute)
	done := false
	for {
		select {
		case request, ok := <-storeRequests:
			if !ok {
				done = true
				break
			}
			log.Printf("new append request for %s. checking if exists in dest.", request.Value)
			// search for in dst
			cmd, err := imap.Wait(dstConn.UIDSearch([]imap.Field{"HEADER", request.Header, request.Value}))
			if err != nil {
				log.Printf("Unable to search for message (%s): %s. Resetting connection.", request.Value, err.Error())
			}

			results := cmd.Data[0].SearchResults()
			// if not found, PULL from SRC and STORE in DST
			if len(results) == 0 {
				log.Printf("%s not in dest. Attempting to append.", request.Value)
				// only fetch if we dont have data already
				if len(request.Msg.Body) == 0 {
					log.Printf("making a fetch request")
					// build and send fetch request
					response := make(chan MessageData)
					fr := fetchRequest{MessageId: request.Value, UID: request.UID, Response: response}
					fetchRequests <- fr

					// grab response from fetchers
					request.Msg = <-response					
				}
				if len(request.Msg.Body) == 0 {
					log.Printf("No data found for from fetch request (%s). giving up", request.Value)
					continue
				}
				
				log.Printf("appending %s", request.Value)
				err = AppendMessage(dstConn, request.Msg)
				if err != nil {
					log.Printf("Problems appending message to dst: %s. quitting.", err.Error())
					return
				}

			}

		case <-timeout.C:
			imap.Wait(dstConn.Noop())
		}
		
		if done {
			break
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

// FetchEmails will sit and wait for fetchRequests from the destination workers.
func fetchEmails(conn *imap.Client, requests chan fetchRequest) {
	// connect to memcached
	cache := memcache.New(MemcacheServer)

	// noop every few to keep things alive
	timeout := time.NewTicker(NoopMinutes * time.Minute)
	done := false
	for {
		select {
		case request, ok := <-requests:
			if !ok {
				done = true
				break
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
				if err == NotFound {
					log.Printf("No data found for UID: %d", request.UID)
				} else {
					log.Printf("Problems fetching message (%s) data: %s. Passing request and quitting.", request.MessageId, err.Error())
					requests <- request
					return
				}
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

		case <-timeout.C:
			imap.Wait(conn.Noop())
		}
		
		if done {
			break
		}
	}

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
