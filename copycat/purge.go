package copycat

import (
	"log"
	"sync"
	"bytes"
	"net/mail"

	"github.com/bradfitz/gomemcache/memcache"
	"code.google.com/p/go-imap/go1/imap"
)

// searchAndPurge will go through the destination inboxes and check if
// each message exists in the source inbox. If a message does not exist
// in the source, delete it from the destination.
func SearchAndPurge(src InboxInfo, dsts []InboxInfo) error {

	// setup pool of 'checkers' to see if messages
	// exist in the source mailbox
	checkRequests := make(chan checkExistsRequest)
	var checkers sync.WaitGroup
	for j := 0; j < MaxImapConns; j++ {
		checkers.Add(1)
		go checkMessagesExist(src, checkRequests, &checkers)
	}

	// setup pool of 'purgers' for each destination
	var purgers sync.WaitGroup
	for _, dst := range dsts {
		purgers.Add(1)
		log.Printf("purge for %s", dst.User)
		go purgeDestination(dst, checkRequests, &purgers)
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
func purgeDestination(dst InboxInfo, checkRequests chan checkExistsRequest, wg *sync.WaitGroup) {
	defer wg.Done()
	
	cmd, err := GetAllMessages(dst)
	if err != nil {
		log.Printf("Unable to find destination messages: %s", err.Error())
	}

	workRequests := make(chan WorkRequest)

	// launch purgers
	var purgers sync.WaitGroup
	for i := 0; i < MaxImapConns; i++ {
		purgers.Add(1)
		go checkAndPurgeMessages(dst, workRequests, checkRequests, &purgers)
	}
	
	// build the requests and send them
	var rsp *imap.Response
	var indx int
	log.Printf("Beginning check/purge for %s with %d messages", dst.User, len(cmd.Data))
	for indx, rsp = range cmd.Data {
		header := imap.AsBytes(rsp.MessageInfo().Attrs["RFC822.HEADER"])
		if msg, _ := mail.ReadMessage(bytes.NewReader(header)); msg != nil {
			header := "Message-Id"
			value := msg.Header.Get(header)

			// create the store request and pass it to each dst's storers
			workRequests <- WorkRequest{Value: value, Header: header, UID: rsp.MessageInfo().UID}
			
			if (indx % 100) == 0 {
				log.Printf("Processed %d messages from %s", indx, dst.User)
			}
		}
	}

	log.Print("purger complete!")
	return
}

func checkAndPurgeMessages(dst InboxInfo, requests chan WorkRequest, checkRequests chan checkExistsRequest, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := GetConnection(dst, false)
	if err != nil {
		log.Print("Problems connecting to destination: %s", err.Error())
		return
	}
	defer conn.Close(true)

	for request := range requests {
		// check and wait for response
		response := make(chan bool)
		cr := checkExistsRequest{UID: request.UID, MessageId: request.Value, Response: response}
		checkRequests <- cr

		// if response is false (does not exist), flag as Deleted
		if exists := <-response; !exists {
			log.Printf("not found in src. marking for deletion: %s", request.Value)
			seqSet, _ := imap.NewSeqSet("")
			seqSet.AddNum(request.UID)
			_, err := conn.UIDStore(seqSet, "+FLAGS", imap.NewFlagSet(`\Deleted`))
			if err != nil {
				log.Printf("Problems removing message from dst: %s", err.Error())
			}
		}
	}
}

type checkExistsRequest struct {
	MessageId string
	UID       uint32
	Response  chan bool
}

func checkMessagesExist(src InboxInfo, checkRequests chan checkExistsRequest, wg *sync.WaitGroup) {
	defer wg.Done()

	// get imap conn
	srcConn, err := GetConnection(src, true)
	if err != nil {
		log.Printf("Problems connecting to source: %s", err.Error())
		return
	}

	// get memcache client
	cache := memcache.New(MemcacheServer)

	for request := range checkRequests {

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
	}
}
