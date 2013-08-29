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
	var nextUID uint32
	if nextUID, err = getNextUID(src); err != nil {
		log.Printf("Unable to get UIDNext: %s", err.Error())
		return err
	}

	// hold the size so we can determine how to react to commands
	startSize := src.Mailbox.Messages

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

			// cache the data so we dont mess it up while start/stopping idle
			var tempData []*imap.Response
			tempData = append(tempData, src.Data...)
			src.Data = nil
			for _, data := range tempData {
				switch data.Type {
				case imap.Data:
					// len of 2 likely means its an EXPUNGE or EXISTS command...
					if len(data.Fields) == 2 {
						msgNum := imap.AsNumber(data.Fields[0])

						switch data.Fields[1] {
						case "EXPUNGE":
							log.Printf("Received an EXPUNGE notification requesting purge - %d", msgNum)
							startSize = msgNum
							requestPurge <- true

						case "EXISTS":
							log.Printf("Received an EXISTS notification - %d", msgNum)
							if startSize > msgNum {
								log.Printf("Mailbox decreased in size %d --> %d. Requesting a purge. MAILBOX MAY NEED TO SYNC", startSize, msgNum)
								requestPurge <- true
								startSize = msgNum
								continue
							}

							// temporarily term the idle so we can fetch the message
							if _, err = src.IdleTerm(); err != nil {
								log.Printf("error while temporarily terminating idle: %s", err.Error())
								return
							}
							log.Printf("terminated idle. appending message.")

							newMessages := msgNum - startSize
							log.Printf("attempting to find/append %d new messages", newMessages)
							for i := uint32(0); i < newMessages; i++ {
								// get message data and append it
								if err = appendNewMessage(src, dsts, nextUID); err != nil {
									log.Printf("error while appending new message: %s. MAILBOXES MAY BE OUT OF SYNC.", err.Error())
								} else {
									nextUID++
									startSize++
								}
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

func appendNewMessage(src *imap.Client, dsts []*imap.Client, uid uint32) (err error) {
	// we want the last appeneded message.
	log.Printf("fetching message (%d) from source", uid)
	//fetch from source
	var msg MessageData
	if msg, err = FetchMessage(src, uid); err != nil {
		log.Printf("errors fetching message: %s", err.Error())
		return err
	}

	var msgId string
	// get the message-id for logging purposes
	if mesg, _ := mail.ReadMessage(bytes.NewReader(msg.Body)); mesg != nil {
		msgId = mesg.Header.Get("Message-Id")
	}

	for _, dst := range dsts {
		if err = AppendMessage(dst, msg); err != nil {
			log.Printf("Problems appeneding new message to destination: %s. INBOXES MAY NOT BE IN SYNC", err.Error())
		} else {
			log.Printf("successfully appended message (%s) to destination. during idle", msgId)
		}
	}

	return nil
}

func getNextUID(conn *imap.Client) (uint32, error) {
	cmd, err := imap.Wait(conn.Status("INBOX", "UIDNEXT"))
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

func sleep(poll chan bool) {
	time.Sleep(10 * time.Second)
	poll <- true
}
