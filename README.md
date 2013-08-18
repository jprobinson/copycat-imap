Copcat IMAP
=============

Copycat is a tool to sync two or more email inboxes. 

It can be ran to sync a single time or run as a background process to sync and then wait for change updates from the server.

-------
-config-file="": Location of a config file to pass in source and destination login information. Use --example-config to see the format.
-dst-host="": The imap host for the destincation mailbox.
-dst-id="": The login ID for the destincation mailbox.
-dst-pw="": The login password for the destincation mailbox.
-idle=false: Sync the mailboxes and then idle and wait for updates.
-log="stderr": Location to write logs to. stderr by default. If set, a HUP signal will handle logrotate.
-src-host="": The imap host for the source mailbox.
-src-id="": The login ID for the source mailbox.	
-src-pw="": The login password for the source mailbox.
    
    