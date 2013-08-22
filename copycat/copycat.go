package copycat

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"sync"

	"code.google.com/p/go-imap/go1/imap"
)

// CopyCat represents a process waiting to copy
type CopyCat struct {
	// hold on in case of need for reconnect
	SourceInfo InboxInfo
	DestInfo   InboxInfo

	// for logging purposes
	Num int
}

// Sync will make sure that the dst inbox looks exactly like the src.
func (c *CopyCat) Sync(wg *sync.WaitGroup) error {
	defer wg.Done()
	return Sync(c.SourceInfo, c.DestInfo)
}

func (c *CopyCat) SyncAndIdle(wg *sync.WaitGroup) (err error) {
	defer wg.Done()

	err = Sync(c.SourceInfo, c.DestInfo)
	if err != nil {
		return
	}

	// idle ... sync on any notification...only if we can make sync superfast.
	return
}

// Sync will make sure that the dst inbox looks exactly like the src.
func Sync(src InboxInfo, dst InboxInfo) (err error) {
	err = doWork(src, dst, true)
	if err != nil {
		log.Print("ERROR: ", err.Error())
		return
	}

	err = doWork(src, dst, false)
	return
}

type Config struct {
	Source InboxInfo
	Dest   []InboxInfo
}

type InboxInfo struct {
	User string
	Pw   string
	Host string
}

func NewInboxInfo(id string, pw string, host string) (info InboxInfo, err error) {
	info = InboxInfo{User: id, Pw: pw, Host: host}
	return info, info.Validate()
}

func (i *InboxInfo) Validate() error {
	if len(i.User) == 0 {
		return errors.New("Login ID is required.")
	}

	if len(i.Pw) == 0 {
		return errors.New("Login Password is required.")
	}

	if len(i.Host) == 0 {
		return errors.New("IMAP Host is required.")
	}

	return nil
}

// doWork kicks off the processes that do part of the work to sync
// 2 inboxes. If purge is true, messages that exist in dst but not
// src will be removed. If purge is false, message that exist in src
// but not dst will be stored in dst.
func doWork(src InboxInfo, dst InboxInfo, purge bool) error {
	allMsgs, _ := imap.NewSeqSet("*")

	var info InboxInfo
	if purge {
		info = dst
	} else {
		info = src
	}

	conn, err := getConnection(info, false)
	if err != nil {
		return err
	}

	// setup workers
	requests := make(chan string)
	var wg sync.WaitGroup
	// TODO: figure out an approp # of workers
	for i := 0; i < 1; i++ {
		wg.Add(1)

		if purge {
			go checkAndPurge(src, dst, requests, &wg)
		} else {
					go checkAndStore(src, dst, requests, &wg)
				}

	}

	// get ALL HEADERS from inbox...
	cmd, err := conn.Fetch(allMsgs, "RFC822.HEADER")
	if err != nil {
		return err
	}

	for cmd.InProgress() {
		// Wait for the next response (no timeout)
		conn.Recv(-1)
		
		// Process command data
		var rsp *imap.Response
		for _, rsp = range cmd.Data {
			header := imap.AsBytes(rsp.MessageInfo().Attrs["RFC822.HEADER"])
			if msg, _ := mail.ReadMessage(bytes.NewReader(header)); msg != nil {
				reqID := msg.Header.Get("Message-Id")

				if len(reqID) == 0 {
					reqID = msg.Header.Get("message-id")
				}

				if len(reqID) == 0 {
					reqID = msg.Header.Get("Message-ID")
				}

				requests <- reqID
			}
		}
		cmd.Data = nil

		// Process unilateral server data...not sure if i need to do this.
		for _, rsp = range conn.Data {
			fmt.Println("Server data:", rsp)
		}
		conn.Data = nil
	}

	// after everything is on the channel, close it...
	close(requests)
	// ... and wait for our workers to finish up.
	wg.Wait()

	if purge {
		// expunge at the end
		_, err := imap.Wait(conn.Expunge(allMsgs))
		if err != nil {
			return err
		}

	}

	return nil
}

// checkAndPurge will pull message message ids off of requests and do some work
func checkAndPurge(src InboxInfo, dst InboxInfo, requests chan string, wg *sync.WaitGroup) {
	defer wg.Done()

	srcConn, dstConn, err := getConnections(src, false, dst, true)
	if err != nil {
		log.Printf("Unable to connect - %s", err.Error())
		return
	}
	defer srcConn.Close(false)
	defer dstConn.Close(true)

	for messageID := range requests {
		// search for in src
		fmt.Print("yup! ", messageID, "\n")

		// if not found, set uid to /Deleted in dst
	}

	return
}

func checkAndStore(src InboxInfo, dst InboxInfo, requests chan string, wg *sync.WaitGroup) {
	defer wg.Done()

	srcConn, dstConn, err := getConnections(src, false, dst, true)
	if err != nil {
		log.Printf("Unable to connect - %s", err.Error())
		return
	}
	defer srcConn.Close(false)
	defer dstConn.Close(true)

	for messageID := range requests {
		// search for UID in dst
		cmd, err := imap.Wait(dstConn.Search("HEADER","Message-Id",messageID))
		if err != nil {
			log.Printf("searcfail: %s", err.Error())
			return 
		}

		// if not found, fetch from src and store in dest
		if len(cmd.Data) == 0 {
			log.Printf("WAH WAHHH")
		} else {
			for data := range cmd.Data {
				fmt.Printf("yessir! %d", data)
			}
		}

	}

	return
}

func getConnections(src InboxInfo, srcReadOnly bool, dst InboxInfo, dstReadOnly bool) (*imap.Client, *imap.Client, error) {
	srcConn, err := getConnection(src, srcReadOnly)
	if err != nil {
		return nil, nil, err
	}

	dstConn, err := getConnection(dst, dstReadOnly)
	if err != nil {
		return nil, nil, err
	}

	return srcConn, dstConn, nil
}

func getConnection(info InboxInfo, readOnly bool) (*imap.Client, error) {
	conn, err := imap.DialTLS(info.Host, new(tls.Config))
	if err != nil {
		return nil, err
	}

	_, err = conn.Login(info.User, info.Pw)
	if err != nil {
		return nil, err
	}

	_, err = imap.Wait(conn.Select("INBOX", readOnly))
	if err != nil {
		return nil, err
	}

	return conn, nil
}
