# Copycat IMAP

Copycat is a tool to replicate an Email inbox to one or more destination inboxes.

It can be run to sync inboxes a single time or as a background process to sync and then wait for change updates from the IMAP server.

-------

### Usage (CLI)
```shell
$./copycat-imap -h
Usage of ./copycat-imap:
  -c=2: The number of concurrent IMAP connections for each inbox during Syncing. Large #s may run faster but you may risk reaching connection/bandwidth limits for you email provider.
  -config-file="": Location of a config file to pass in source and destination login information. Use -example-config to see the format.
  -db="/var/copycat/messages": path for message storage
  -dst-host="": The imap host for the destincation mailbox.
  -dst-id="": The login ID for the destincation mailbox.
  -dst-pw="": The login password for the destincation mailbox.
  -example-config=false: View an example layout for a json config file meant to hold multiple destination accounts.
  -idle=false: Sync the mailboxes and then idle and wait for updates. Creates an additional connection for each inbox.
  -log="": Location to write logs to. stderr by default. If set, a HUP signal will handle logrotate.
  -purge=false: During the sync this will purge any destination messages that do not exist in the source.
  -quick=false: Starts a quick sync that will only look to 'sync' the last 'quick-count' messages.
  -quick-count=500: The number of messages to look for with a quick scan.
  -src-host="": The imap host for the source mailbox.
  -src-id="": The login ID for the source mailbox.
  -src-pw="": The login password for the source mailbox.
  -sync=true: Run a sync of the mailboxes. Flag helpful for skipping sync with bandwidth usage is limited.
```

#### Credentials
* Passed via command line (src-id|src-pw|src-host & dst-id|dst-pw|dst-host)
* ...or via a JSON config file. Format is described with the -example-config option:

```shell
$./copycat-imap -example-config
	{
	    "source": {
	        "user": "source_user_name",
	        "pw": "source_pa$$w0rd",
	        "host": "imap.source.com"
	    },
	    "dest": [
	        {
	            "user": "dest1_user_name",
	            "pw": "dest1_pa$$w0rd",
	            "host": "imap.dest1.com"
	        },
	        {
	            "user": "dest2_user_name",
	            "pw": "dest2_pa$$w0rd",
	            "host": "imap.dest2.com"
	        }
	    ]
	}
```

#### Sync
If the -sync parameter is set, copycat will purge any messages in the destinations that do not exist in the source and then verify that all messages in the source exist in the destinations. Any missing messages will be appeneded to the destinations with only the 'UnSeen' flag set. Message flags in the source WILL NOT be retained on the copy.

#### Quick Sync
If you only want to run sync over the latest N messages, set quick=true and set N with the quick-count param. Great if you know most of your inbox is mostly synced and just want to catch up every now and then. 

#### Daemon Mode (IDLE)
If the -idle parameter is set, copycat will perform a Sync and setup connections to IDLE on a source inbox connection. It will run like this indefinitely and will propagate changes to the destination inboxes until the process is killed.

#### Logging
Logs will be sent to stderr unless specified with the -log parameter. If set, a SIGHUP signal can be sent to the process on postrotate.

#### Limitations
So far, this tool has only been tested with GMail accounts. In order for Copycat-IMAP to work, the Email provider must support 'Message-Id' headers, message UIDs and IDLE. The tool is not setup to detect if the Email provider does not support these so please verify on your own before using the tool. 

#### Dependencies
To limit precious IMAP bandwidth usage (even GMail only allows ~2.8GB transfers via IMAP per day), CopyCat uses goleveldb to store messages by their Message-Id locally.

This tool makes use of a couple external libraries that you'll need to 'go get' if you plan on using it as a library:

* [Go-IMAP](https://code.google.com/p/go-imap/)
* [goleveldb](https://github.com/syndtr/goleveldb/leveldb)
    
    
