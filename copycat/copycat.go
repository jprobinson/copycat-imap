package copycat

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/sbinet/go-imap/go1/imap"
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
	err = doWork(src, dst, false)
	if err != nil {
		return
	}

	err = doWork(src, dst, true)
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

	conn, err := getConnection(info)
	if err != nil {
		return err
	}

	// get ALL UID dst...
	cmd, err := imap.Wait(conn.UIDFetch(allMsgs, "ALL"))
	if err != nil {
		return err
	}

	// setup handlers
	requests := make(chan uint32)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		if purge {
			go checkAndPurge(src, dst, requests, &wg)
		} else {
			go checkAndStore(src, dst, requests, &wg)
		}

	}

	// pass UIDs off to the workers to do their thing
	for _, resp := range cmd.Data {
		requests <- resp.MessageInfo().UID
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

// checkAndPurge will pull message UIDs off of
func checkAndPurge(src InboxInfo, dst InboxInfo, requests chan uint32, wg *sync.WaitGroup) {
	defer wg.Done()

	srcConn, dstConn, err := getConnections(src, dst)
	if err != nil {
		log.Printf("Unable to connect - %s", err.Error())
		return
	}
	defer srcConn.Close(true)
	defer dstConn.Close(true)

	for requestUID := range requests {
		// search for UID in src
		fmt.Print(requestUID,"\n")

		// if not found, set uid to /Deleted in dst
	}

	return
}

func checkAndStore(src InboxInfo, dst InboxInfo, requests chan uint32, wg *sync.WaitGroup) {
	defer wg.Done()

	srcConn, dstConn, err := getConnections(src, dst)
	if err != nil {
		log.Printf("Unable to connect - %s", err.Error())
		return
	}
	defer srcConn.Close(true)
	defer dstConn.Close(true)

	for requestUID := range requests {
		uidSeq, err := imap.NewSeqSet(fmt.Sprintf("%d", requestUID))
		if err != nil {
			log.Printf("problems setting up uid search: %s", err.Error())
			continue
		}

		// search for UID in dst
		cmd, err := imap.Wait(dstConn.UIDSearch(uidSeq))

		// if not found, fetch from src and store in dest
		if len(cmd.Data) == 0 {

		}

	}

	return
}

func getConnections(src InboxInfo, dst InboxInfo) (*imap.Client, *imap.Client, error) {
	srcConn, err := getConnection(src)
	if err != nil {
		return nil, nil, err
	}

	dstConn, err := getConnection(dst)
	if err != nil {
		return nil, nil, err
	}

	return srcConn, dstConn, nil
}

func getConnection(info InboxInfo) (*imap.Client, error) {
	conn, err := imap.DialTLS(info.Host, new(tls.Config))
	if err != nil {
		return nil, err
	}

	_, err = conn.Login(info.User, info.Pw)
	if err != nil {
		return nil, err
	}

	_, err = imap.Wait(conn.Select("INBOX", false))
	if err != nil {
		return nil, err
	}

	return conn, nil
}
