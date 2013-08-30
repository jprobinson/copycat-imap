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
func Idle(src *imap.Client, appendRequests []chan WorkRequest, requestPurge chan bool) (err error) {
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
								var request WorkRequest
								if request, err = getMessageInfo(src, nextUID); err == nil {

									log.Printf("creating %d append requests for %d", len(appendRequests), nextUID)
									for _, requests := range appendRequests {
										requests <- request
									}
									log.Printf("done creating append requests for %d", nextUID)
									nextUID++
									startSize++
								} else {
									log.Printf("Unable to find message for UID (%d): %s", nextUID, err.Error())
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

func getMessageInfo(conn *imap.Client, uid uint32) (WorkRequest, error) {
	log.Printf("fetching data for (%d) from src for idle", uid)

	// get headers and UID for ALL message in src inbox...
	msg, err := FetchMessage(conn, uid)
	if err != nil {
		return WorkRequest{}, err
	}

	var request WorkRequest
	if mesg, _ := mail.ReadMessage(bytes.NewReader(msg.Body)); mesg != nil {
		header := "Message-Id"
		value := mesg.Header.Get(header)
		request = WorkRequest{Value: value, Header: header, UID: uid, Msg: msg}
	} else {
		return request, errors.New("message was empty")
	}

	log.Printf("fetched data for %d!", uid)
	return request, nil
}

// getNextUID will grab the next message UID from the inbox. Client.Mailbox.UIDNext is cached so we can't use it.
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

// sleep is for sleeping. zZZzzZZzzZZzzz
func sleep(poll chan bool) {
	time.Sleep(10 * time.Second)
	poll <- true
}
