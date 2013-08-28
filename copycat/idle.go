package copycat

import (
	"bytes"
	"errors"
	"log"
	"net/mail"
	"os"
	"os/signal"
	"time"

	"code.google.com/p/go-imap/go1/imap"
)

const idleTimeoutMinutes = 20

// Idle setup the processes to wait for notifications from the IMAP source connection.
// If an EXISTS or EXPUNGE command comes across the pipe, the appropriate actions will be
// taken to update the destinations. If the process decides the inboxes are out of sync,
// it will pass a bool to the requestPurge channel. It is expected that the requestPurge
// channel is setup to initiate a purge process when it receives the notificaiton.
func Idle(src *imap.Client, dsts []*imap.Client, requestPurge chan bool) (err error) {
	// uid->message-Id map for handling EXPUNGE commands
	cache, err := createUIDMap(src)
	if err != nil {
		return err
	}
	// hold the size so we can determine how to react to commands
	mailboxSize := uint32(len(cache))

	// get next UID for appending (handling EXISTS commands)
	nextUID, err := getNextUID(src)
	if err != nil {
		log.Printf("Unable to get next UID: %s", err.Error())
		return err
	}

	// setup interrupt signal channel to terminate the idle
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill)

	// setup ticker to reset the idle every 20 minutes (RFC-2177 recommends 29 mins max)
	timeout := time.NewTicker(idleTimeoutMinutes * time.Minute)

	// setup poller signal for checking for data on the idle command
	poll := make(chan bool, 1)
	poll <- true

	log.Print("beginning idle...")
	_, idleErr := src.Idle()
	if (idleErr != nil) && (idleErr != imap.ErrTimeout) {
		log.Printf("Idle error: %s", idleErr.Error())
		return
	}

	for {

		select {
		// if we receive a 'poll' we should check the pipe for new messages
		case <-poll:

			err = src.Recv(0)
			if (idleErr != nil) && (idleErr != imap.ErrTimeout) {
				log.Printf("Idle error: %s", idleErr.Error())
				go sleep(poll)
				continue
			}

			for _, data := range src.Data {
				switch data.Type {
				case imap.Data:
					// len of 2 likely means its an EXPUNGE or EXISTS command...
					if len(data.Fields) == 2 {
						msgNum := imap.AsNumber(data.Fields[0])

						switch data.Fields[1] {
						case "EXPUNGE":
							log.Printf("Received an EXPUNGE notification - %d", msgNum)
							// use our handy-dandy map[uid]messageId to lookup and purge
							if messageId, exists := cache[msgNum]; exists {
								expungeMessage(dsts, messageId)
							} else {
								log.Printf("messaged (%d) does not exist in cache. Requesting a purge.")
								requestPurge <- true
							}

						case "EXISTS":
							log.Print("Received an EXISTS notification")
							if mailboxSize > msgNum {
								log.Printf("Mailbox decreased in size %d --> %d. Requesting a purge.", mailboxSize, msgNum)
								requestPurge <- true
								mailboxSize = msgNum
								continue
							}

							// temporarily term the idle so we can fetch the message
							if _, err = src.IdleTerm(); err != nil {
								log.Printf("error while temporarily terminating idle: %s", err.Error())
								return
							}
							log.Printf("terminated idle. appending message.")

							// get message data and append it
							if err = appendNewMessage(src, dsts, cache, nextUID); err == nil {
								nextUID++
							} else {
								log.Printf("error while appending new message (%d): %s. MAILBOXES MAY BE OUT OF SYNC.", nextUID, err.Error())
							}

							log.Printf("continuing idle...")
							// turn idle back on
							if _, err = src.Idle(); err != nil {
								log.Printf("Unable to restart idle: %s", err.Error())
								return
							}
						}
					}
				}
			}
			src.Data = nil
			go sleep(poll)

		case <-interrupt:
			log.Printf("Received interrupt. Terminating idle...")
			_, err = src.IdleTerm()
			if err != nil {
				log.Printf("error while terminating idle: %s", err.Error())
			}
			return
		case <-timeout.C:
			log.Printf("resetting idle...")
			_, err = src.IdleTerm()
			if err != nil {
				log.Printf("error while temporarily terminating idle: %s", err.Error())
				return
			}
			log.Printf("terminated idle.")

			// turn idle back on
			_, err = src.Idle()
			if err != nil {
				log.Printf("Unable to restart idle: %s", err.Error())
				return
			}
			log.Printf("idle restarted.")
		}
	}

	return
}

func sleep(poll chan bool) {
	time.Sleep(20 * time.Second)
	poll <- true
}

func createUIDMap(conn *imap.Client) (map[uint32]string, error) {
	cache := make(map[uint32]string)
	log.Print("creating UID->Message-Id source cache...")
	cmd, err := GetAllMessages(conn)
	if err != nil {
		log.Print("Problems creating cache: %s", err.Error())
		return cache, err
	}

	for _, rsp := range cmd.Data {
		header := imap.AsBytes(rsp.MessageInfo().Attrs["RFC822.HEADER"])
		if msg, _ := mail.ReadMessage(bytes.NewReader(header)); msg != nil {
			msgId := msg.Header.Get("Message-Id")
			uid := rsp.MessageInfo().UID
			cache[uid] = msgId
		}
	}
	log.Printf("cache filled with %d messages", len(cache))
	return cache, nil
}

func expungeMessage(dsts []*imap.Client, messageId string) {
	// search for message data to pull UID for each mailbox, then delete/expunge
	for _, dst := range dsts {
		// search for in dst to find UID
		cmd, err := imap.Wait(dst.UIDSearch([]imap.Field{"HEADER", "Message-Id", messageId}))
		if err != nil {
			log.Printf("Unable to search for message (%s): %s", messageId, err.Error())
			return
		}

		results := cmd.Data[0].SearchResults()
		// if not found, give up and move on
		if len(results) == 0 {
			log.Printf("Message (%s) not found in dst", messageId)
			return
		}

		// add deleted flag
		err = AddDeletedFlag(dst, results[0])
		if err != nil {
			log.Printf("Problems removing expunged message (UID:%d) from destination: %s", results[0], err.Error())
		}

		// expunge
		allMsgs, _ := imap.NewSeqSet("")
		allMsgs.Add("1:*")
		imap.Wait(dst.Expunge(allMsgs))
	}
}

func appendNewMessage(src *imap.Client, dsts []*imap.Client, cache map[uint32]string, uid uint32) error {
	log.Printf("fetching message (%d) from source", uid)
	//fetch from source
	msg, err := FetchMessage(src, uid)
	if err != nil {
		log.Printf("errors fetching message: %s", err.Error())
		return err
	}

	// get the message-id and toss it in the cache
	if msg, _ := mail.ReadMessage(bytes.NewReader(msg.Body)); msg != nil {
		msgId := msg.Header.Get("Message-Id")
		cache[uid] = msgId
		log.Printf("Set new cache value [%d]=%s", uid, msgId)
	}

	for _, dst := range dsts {
		if err := AppendMessage(dst, msg); err != nil {
			log.Printf("Problems appeneding new message to destination: %s. INBOXES MAY NOT BE IN SYNC", err.Error())
		}
	}

	return nil
}

func getNextUID(src *imap.Client) (uint32, error) {
	cmd, err := imap.Wait(src.Status("INBOX", "UIDNEXT"))
	if err != nil {
		return 0, err
	}

	if len(cmd.Data) == 0 {
		return 0, errors.New("no data returned!")
	}

	var status *imap.MailboxStatus
	for _, resp := range cmd.Data {
		switch resp.Type {
		case imap.Data:
			status = resp.MailboxStatus()
			if status != nil {
				break
			}
		}
	}

	return status.UIDNext, nil
}
