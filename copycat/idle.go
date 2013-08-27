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

// Idle will make sure that the dst inbox looks exactly like the src.
func Idle(src *imap.Client, dsts []*imap.Client) (err error) {
	cache, err := createUIDMap(src)
	if err != nil {
		return err
	}

	// get next UID for appending
	nextUID, err := getNextUID(src)
	if err != nil {
		log.Printf("Unable to get next UID: %s", err.Error())
		return err
	}
	log.Printf("Grabbed next UID: %d", nextUID)

	// setup interrupt signal channel to terminate the idle
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, os.Kill)

	// setup poller signal for checking for data
	poll := make(chan bool, 1)
	poll <- true

	log.Print("beginning idle...")
	_, idleErr := src.Idle()
	if idleErr != nil {
		log.Printf("Idle fail: %s", idleErr.Error())
		return
	}

	for {

		select {
		case <-poll:

			log.Print("checking for changes...")
			err = src.Recv(0)
			if err != nil {
				log.Printf("Ran into error: %s", err.Error())
				goto wait
			}

			for _, data := range src.Data {
				switch data.Type {
				case imap.Data:
					// len of 2 likely means its an EXPUNGE or EXISTS command...
					if len(data.Fields) == 2 {
						msgUID := imap.AsNumber(data.Fields[0])

						switch data.Fields[1] {
						case "EXPUNGE":
							log.Print("Received an EXPUNGE notification")
							// use our handy-dandy map[uid]messageId to lookup and purge
							if messageId, exists := cache[msgUID]; exists {
								expungeMessage(dsts, messageId)
							}

						case "EXISTS":
							log.Print("Received an EXISTS notification")
							// temporarily term the idle so we can fetch the message
							_, err = src.IdleTerm()
							if err != nil {
								log.Printf("error while temporarily terminating idle: %s", err.Error())
								return
							}
							log.Printf("terminated idle. appending message.")

							// get message data and append it
							err = appendNewMessage(src, dsts, nextUID)
							if err != nil {
								log.Printf("error while appending new message (%d): %s. MAILBOXES MAY BE OUT OF SYNC.", msgUID, err.Error())
								continue
							}
							nextUID++

							log.Printf("continuing idle...")

							// turn idle back on
							_, err = src.Idle()
							if err != nil {
								log.Printf("Unable to restart idle: %s", err.Error())
								return
							}
						}
					}
				case imap.Status:

				}
			}
			src.Data = nil
		wait:
			go func() {
				time.Sleep(20 * time.Second)
				poll <- true
			}()

		case <-interrupt:
			log.Printf("Received interrupt. Terminating idle...")
			_, err = src.IdleTerm()
			if err != nil {
				log.Printf("error while terminating idle: %s", err.Error())
			}
			return
		}
	}

	return
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
			continue
		}

		results := cmd.Data[0].SearchResults()
		// if not found, give up and move on
		if len(results) == 0 {
			log.Printf("Message (%s) not found in dst", messageId)
			continue
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

func appendNewMessage(src *imap.Client, dsts []*imap.Client, uid uint32) error {
	log.Printf("fetching message (%d) from source", uid)
	//fetch from source
	msg, err := FetchMessage(src, uid)
	if err != nil {
		log.Printf("errors fetching message: %s", err.Error())
		return err
	}

	for _, dst := range dsts {
		err := AppendMessage(dst, msg)
		if err != nil {
			log.Printf("Problems appeneding new message to destination: %s. INBOXES MAY NOT BE IN SYNC", err.Error())
		}
	}

	return nil
}

func getNextUID(src *imap.Client) (uint32, error) {
	cmd, err := src.Status("INBOX")
	if err != nil {
		return 0, err
	}

	if len(cmd.Data) == 0 {
		return 0, errors.New("no data returned!")
	}

	log.Printf("src data lenght %d", len(src.Data))

	var status *imap.MailboxStatus
	for _, resp := range cmd.Data {
		for _, f := range resp.Fields {
			log.Printf("fields: %s", imap.AsString(f))
		}
		switch resp.Type {
		case imap.Status:
			log.Printf("got a status")
			status = resp.MailboxStatus()
			if status != nil {
				log.Print("BREAK")
				break
			}

		default:
			log.Printf("got a %s", resp.Type)
		}
	}

	return status.UIDNext, nil
}
