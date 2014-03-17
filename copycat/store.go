package copycat

import (
	"bytes"
	"log"
	"net/mail"
	"sync"
	"time"

	"code.google.com/p/go-imap/go1/imap"
)

// SearchAndStore will check check if each message in the source inbox
// exists in the destinations. If it doesn't exist in a destination, the message info will
// be pulled and stored into the destination.
func SearchAndStore(src []*imap.Client, dsts map[string][]*imap.Client, dbFile string, quickSyncCount int) (err error) {
	var cmd *imap.Command
	cmd, err = GetAllMessages(src[0])
	if err != nil {
		log.Printf("Unable to get all messages!")
		return
	}

	// connect to cache
	cache, err := NewCache(dbFile)
	if err != nil {
		log.Printf("problems initiating cache - %s", err.Error())
		return
	}
	defer cache.Close()

	// setup message fetchers to pull from the source/memcache
	fetchRequests := make(chan fetchRequest)
	for _, srcConn := range src {
		go fetchEmails(srcConn, fetchRequests, cache)
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
	log.Printf("store processing for %d messages from the source inbox", len(cmd.Data))
	var rsp *imap.Response
	var indx int
	startTime := time.Now()
	syncStart := 0
	// consider quick sync
	if quickSyncCount != 0 {
		syncStart = len(cmd.Data) - quickSyncCount
		log.Printf("found quick sync count. will only sync messages %d through %d", syncStart, len(cmd.Data))
	}
	for indx, rsp = range cmd.Data[syncStart:] {
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
			// search for in dst
			cmd, err := imap.Wait(dstConn.UIDSearch([]imap.Field{"HEADER", request.Header, request.Value}))
			if err != nil {
				log.Printf("Unable to search for message (%s): %s. skippin!", request.Value, err.Error())
				continue
			}

			results := cmd.Data[0].SearchResults()
			// if not found, PULL from SRC and STORE in DST
			if len(results) == 0 {
				// only fetch if we dont have data already
				if len(request.Msg.Body) == 0 {
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
func fetchEmails(conn *imap.Client, requests chan fetchRequest, cache *Cache) {

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
			found := true
			// check if the message body is in cache
			data, err := cache.Get(request.MessageId)
			if err != nil {
				found = false
				if err != ErrNotFound {
					log.Printf("problems pulling message data from cache: %s. Pulling message from src...", err.Error())
				}
				data = MessageData{}
			}

			if found {
				log.Print("cache success!")
				request.Response <- data
				continue
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

			err = cache.Put(request.MessageId, msgData)
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
