package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/jackc/pgconn"
	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgproto3/v2"
	"github.com/sirupsen/logrus"
)

const CONN = "postgres://postgres:password@localhost/postgres?replication=database"
const SLOT_NAME = "test_slot3"
const OUTPUT_PLUGIN = "pgoutput"

var ACTOR = struct {
	Relation string
	Columns  []string
}{}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	/*
			   notice the drop down from pgx to pgconn,
		     this is due to protocol differences when consuming logical replication changesets vs simple SQL execution
	*/
	conn, err := pgconn.Connect(ctx, CONN)
	if err != nil {
		panic(err)
	}
	defer conn.Close(ctx)

	// 1. ensure publication exists
	if _, err := conn.Exec(ctx, "DROP PUBLICATION IF EXISTS gizmo_pub").ReadAll(); err != nil {
		logrus.WithError(err).Fatal("failed to drop publication error")
	}

	if _, err := conn.Exec(ctx, "CREATE PUBLICATION gizmo_pub FOR TABLE gizmos").ReadAll(); err != nil {
		logrus.WithError(err).Fatal("failed to create publication")
	}

	// 2. create temproary replication slot server
	if _, err = pglogrepl.CreateReplicationSlot(ctx, conn, SLOT_NAME, OUTPUT_PLUGIN, pglogrepl.CreateReplicationSlotOptions{Temporary: true}); err != nil {
		logrus.WithError(err).Fatal("failed to create a replication slot")
	}

	var msgPointer pglogrepl.LSN
	pluginArguments := []string{"proto_version '1'", "publication_names 'gizmo_pub'"}

	// 3. establish connection
	err = pglogrepl.StartReplication(ctx, conn, SLOT_NAME, msgPointer, pglogrepl.StartReplicationOptions{PluginArgs: pluginArguments})
	if err != nil {
		logrus.WithError(err).Fatal("failed to establish start replication")
	}

	var pingTime time.Time
	for ctx.Err() != context.Canceled {
		if time.Now().After(pingTime) {
			if err = pglogrepl.SendStandbyStatusUpdate(ctx, conn, pglogrepl.StandbyStatusUpdate{WALWritePosition: msgPointer}); err != nil {
				logrus.WithError(err).Fatal("failed to send standby update")
			}
			pingTime = time.Now().Add(10 * time.Second)
			logrus.Debug("client: please standby")
		}

		ctx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()

		msg, err := conn.ReceiveMessage(ctx)
		if pgconn.Timeout(err) {
			continue
		}
		if err != nil {
			logrus.WithError(err).Fatal("something went while listening for message")
		}

		switch msg := msg.(type) {
		case *pgproto3.CopyData:
			switch msg.Data[0] {
			case pglogrepl.PrimaryKeepaliveMessageByteID:
				logrus.Debug("server: confirmed standby")

			case pglogrepl.XLogDataByteID:
				walLog, err := pglogrepl.ParseXLogData(msg.Data[1:])
				if err != nil {
					logrus.WithError(err).Fatal("failed to parse logical WAL log:", err)
				}

				var msg pglogrepl.Message
				if msg, err = pglogrepl.Parse(walLog.WALData); err != nil {
					logrus.WithError(err).Fatalf("failed to parse logical replication message")
				}

				/*
				   simply logging here, but could easily push onto a message queue or something
				*/
				switch m := msg.(type) {
				case *pglogrepl.RelationMessage:
					ACTOR.Columns = []string{}
					for _, col := range m.Columns {
						ACTOR.Columns = append(ACTOR.Columns, col.Name)
					}
					ACTOR.Relation = m.RelationName
				case *pglogrepl.InsertMessage:
					var sb strings.Builder
					sb.WriteString(fmt.Sprintf("INSERT %s(", ACTOR.Relation))
					for i := 0; i < len(ACTOR.Columns); i++ {
						sb.WriteString(fmt.Sprintf("%s: %s", ACTOR.Columns[i], string(m.Tuple.Columns[i].Data)))
					}
					sb.WriteString(")\n")
					logrus.Info(sb.String())
				case *pglogrepl.DeleteMessage:
					logrus.Info("DELETE")
				case *pglogrepl.TruncateMessage:
					logrus.Info("ALL GONE (TRUNCATE)")
				}
			}
		default:
			logrus.Warnf("received unexpected message: %T\n", msg)
		}

	}
}
