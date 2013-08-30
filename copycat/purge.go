package copycat

import (
	"bytes"
	"log"
	"net/mail"
	"sync"
	"time"

	"code.google.com/p/go-imap/go1/imap"
	"github.com/bradfitz/gomemcache/memcache"
)

// SearchAndPurge will go through the destination inboxes and check if
// each message exists in the source inbox. If a message does not exist
// in the source, delete it from the destination.
func SearchAndPurge(src []*imap.Client, dsts map[string][]*imap.Client) error {

	// setup pool of 'checkers' to see if messages
	// exist in the source mailbox
	checkRequests := make(chan checkExistsRequest)
	var checkers sync.WaitGroup
	for _, srcConn := range src {
		checkers.Add(1)
		go checkMessagesExist(srcConn, checkRequests, &checkers)
	}

	// setup pool of 'purgers' for each destination
	var purgers sync.WaitGroup
	for user, dst := range dsts {
		purgers.Add(1)
		go purgeDestination(user, dst, checkRequests, &purgers)
	}

	// wait for the purgers to complete
	purgers.Wait()
	// clean up checkers once purging complete
	close(checkRequests)
	// ...and wait for our checkers to complete
	checkers.Wait()

	log.Printf("search and purge complete")
	return nil
}

// checkAndPurge will pull message message ids off of requests and do some work
func purgeDestination(user string, dsts []*imap.Client, checkRequests chan checkExistsRequest, wg *sync.WaitGroup) {
	defer wg.Done()

	cmd, err := GetAllMessages(dsts[0])
	if err != nil {
		log.Printf("Unable to find destination messages: %s", err.Error())
	}

	workRequests := make(chan WorkRequest)

	// launch purgers
	var purgers sync.WaitGroup
	for _, dstConn := range dsts {
		purgers.Add(1)
		go checkAndPurgeMessages(dstConn, workRequests, checkRequests, &purgers)
	}

	// build the requests and send them
	var rsp *imap.Response
	var indx int
	startTime := time.Now()
	log.Printf("Beginning check/purge for %s with %d messages", user, len(cmd.Data))
	for indx, rsp = range cmd.Data {
		header := imap.AsBytes(rsp.MessageInfo().Attrs["RFC822.HEADER"])
		if msg, _ := mail.ReadMessage(bytes.NewReader(header)); msg != nil {
			header := "Message-Id"
			value := msg.Header.Get(header)

			// create the store request and pass it to each dst's storers
			workRequests <- WorkRequest{Value: value, Header: header, UID: rsp.MessageInfo().UID}

			if ((indx % 100) == 0) && (indx > 0) {
				since := time.Since(startTime)
				rate := 100 / since.Seconds()
				startTime = time.Now()
				log.Printf("Processed %d messages from %s. Rate: %f msg/s", indx, user, rate)
			}
		}
	}
	log.Printf("Done passing purge requests for %s", user)
	close(workRequests)
	purgers.Wait()

	return
}

func checkAndPurgeMessages(conn *imap.Client, requests chan WorkRequest, checkRequests chan checkExistsRequest, wg *sync.WaitGroup) {
	defer wg.Done()
	
	timeout := time.NewTicker(NoopMinutes * time.Minute)
	done := false
	for {
		select {
		case request, ok := <- requests:
			if !ok {
				done = true
				break
			}
			// check and wait for response
			response := make(chan bool)
			cr := checkExistsRequest{UID: request.UID, MessageId: request.Value, Response: response}
			checkRequests <- cr

			// if response is false (does not exist), flag as Deleted
			if exists := <-response; !exists {
				log.Printf("not found in src. marking for deletion: %s", request.Value)
				err := AddDeletedFlag(conn, request.UID)
				if err != nil {
					log.Printf("Problems removing message from dst: %s", err.Error())
				}
			}
		case <- timeout.C:
			imap.Wait(conn.Noop())
		}
		
		if done {
			break
		}
	}
	
	log.Printf("expunging...")
	// expunge at the end
	allMsgs, _ := imap.NewSeqSet("")
	allMsgs.Add("1:*")
	imap.Wait(conn.Expunge(allMsgs))
	log.Printf("expunge complete.")
}

type checkExistsRequest struct {
	MessageId string
	UID       uint32
	Response  chan bool
}

func checkMessagesExist(srcConn *imap.Client, checkRequests chan checkExistsRequest, wg *sync.WaitGroup) {
	defer wg.Done()
	// get memcache client
	cache := memcache.New(MemcacheServer)
	
	timeout := time.NewTicker(NoopMinutes * time.Minute)
	done := false
	for {
		select {
		case request, ok := <- checkRequests:
			if !ok {
				done = true
				break
			}
			// check if it exists in src
			// search for in src
			cmd, err := imap.Wait(srcConn.UIDSearch([]imap.Field{"HEADER", "Message-Id", request.MessageId}))
			if err != nil {
				log.Printf("Unable to search source: %s", err.Error())
				request.Response <- false
				continue
			}

			results := cmd.Data[0].SearchResults()
			// if not found, mark for deletion in DST
			found := (len(results) > 0)

			// response with found bool
			request.Response <- found

			// if it doesnt exist, attempt to remove it from memcached
			if !found {
				cache.Delete(request.MessageId)
			}			
		case <- timeout.C:
			imap.Wait(srcConn.Noop())
		}
		
		if done {
			break
		}
	}

}
