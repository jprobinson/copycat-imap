Copycat IMAP
=============

Copycat is a tool to sync two or more email inboxes. 

It can be ran to sync a single time or run as a background process to sync and then wait for change updates from the server.

-------

>-src-id="": The login ID for the source mailbox.	

>-src-pw="": The login password for the source mailbox.

>-src-host="": The imap host for the source mailbox.

>-dst-id="": The login ID for the destincation mailbox.

>-dst-pw="": The login password for the destincation mailbox.

> -dst-host="": The imap host for the destincation mailbox.

> -config-file="": Location of a config file to pass in source and destination login information. Use --example-config to see the format.

>  -example-config=false: View an example layout for a json config file meant to hold multiple destination accounts.

>-idle=false: Sync the mailboxes and then idle and wait for updates.

>-log="stderr": Location to write logs to. stderr by default. If set, a HUP signal will handle logrotate.




    
    