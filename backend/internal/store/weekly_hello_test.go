package store

import "testing"

func TestMergedHelloResultsAreAtomic(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			if _, err = s.DB.Exec(`INSERT INTO auto_hello_state(account_id,limit_id,window_type,started_at)
				VALUES(1,'codex','primary',1000);
				INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,body,account_id) VALUES
				('weekly','codex','auto_hello_weekly','pending',1000,'Hello',1),
				('idle','codex','auto_hello','pending',1000,'Hello',1)`); err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err = s.DB.Exec(`CREATE TRIGGER fail_episode BEFORE UPDATE ON auto_hello_state
					BEGIN SELECT RAISE(ABORT,'test episode failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			completed := int64(1003)
			err = s.RecordAutoHelloResults([]AutoHelloTask{
				{Key: "weekly", AccountID: 1, LimitID: "codex", WindowType: "secondary", ScheduledAt: 1000, Weekly: true},
				{Key: "idle", AccountID: 1, LimitID: "codex", WindowType: "primary", ScheduledAt: 1000},
			}, "sent", 1, "", &completed, completed)
			if (err != nil) != fail {
				t.Fatalf("result error=%v; want failure=%v", err, fail)
			}
			status, logs, episodes := "sent", 1, 1
			if fail {
				status, logs, episodes = "pending", 0, 0
			}
			for _, check := range []struct {
				query string
				want  int
			}{
				{"SELECT COUNT(*) FROM notifications WHERE status='" + status + "'", 2},
				{"SELECT COUNT(*) FROM auto_hello_logs", logs},
				{"SELECT COUNT(*) FROM auto_hello_state WHERE completed_at IS NOT NULL", episodes},
			} {
				var count int
				if err = s.DB.QueryRow(check.query).Scan(&count); err != nil || count != check.want {
					t.Fatalf("%s: count=%d err=%v; want %d", check.query, count, err, check.want)
				}
			}
		})
	}
}

func TestWeeklyHelloAccountCleanup(t *testing.T) {
	for _, action := range []string{"identity change", "disconnect", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			second, err := s.CreateAccount("second")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(`INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
				VALUES('first','codex','auto_hello_weekly','pending',1,'Hello',1),
				('second','codex','auto_hello_weekly','pending',1,'Hello',?)`, second.ID); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "identity change":
				err = s.DeleteAutoHelloData(second.ID)
			case "disconnect":
				err = s.DisconnectAccount(second.ID)
			case "delete":
				err = s.DeleteAccount(second.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			var key string
			if err = s.DB.QueryRow("SELECT dedupe_key FROM notifications").Scan(&key); err != nil || key != "first" {
				t.Fatalf("remaining task=%q err=%v; want first", key, err)
			}
			var count int
			if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil || count != 1 {
				t.Fatalf("remaining tasks=%d err=%v; want 1", count, err)
			}
		})
	}
}
