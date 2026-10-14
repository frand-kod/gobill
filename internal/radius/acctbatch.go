package radius

import (
	"context"

	"github.com/frand-kod/gobill/internal/db"
)

// acctBatchMax caps how many accounting writes share one commit.
const acctBatchMax = 256

// acctJob is one accounting write waiting for the batch writer.
type acctJob struct {
	write func(q *db.Queries) error
	done  chan error
}

// commit runs write in a transaction shared with the other accounting writes waiting at that moment
// (group commit), and returns once that transaction is durable. Every commit costs an fsync, about
// 5 ms on a laptop NVMe and tens of ms on the SD card or eMMC of an ARM box; one commit per packet
// capped accounting at 200 packets/s even on the NVMe. Batching costs nothing when idle: a lone
// packet is written at once, and batches only grow while the previous commit is still on disk.
// Without DB (tests, the REST fallback server) the write runs directly on Q.
func (s *Server) commit(ctx context.Context, write func(q *db.Queries) error) error {
	if s.DB == nil {
		return write(s.Q)
	}
	s.acctOnce.Do(func() {
		s.acctQ = make(chan acctJob, acctBatchMax)
		go s.acctWriter()
	})
	j := acctJob{write: write, done: make(chan error, 1)}
	select {
	case s.acctQ <- j:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-j.done:
		return err
	case <-ctx.Done(): // the write may still land; the NAS retransmits and the upsert is idempotent
		return ctx.Err()
	}
}

// acctWriter takes the first waiting job, every job queued behind it up to acctBatchMax, and commits
// them together. When the batch fails, each job is retried in its own transaction, so one bad write
// cannot fail the others; a job that still fails gets its error and its NAS retransmits.
func (s *Server) acctWriter() {
	for j := range s.acctQ {
		batch := []acctJob{j}
	fill:
		for len(batch) < acctBatchMax {
			select {
			case j := <-s.acctQ:
				batch = append(batch, j)
			default:
				break fill
			}
		}
		if err := s.writeBatch(batch); err == nil || len(batch) == 1 {
			for _, j := range batch {
				j.done <- err
			}
			continue
		}
		for _, j := range batch {
			j.done <- s.writeBatch([]acctJob{j})
		}
	}
}

func (s *Server) writeBatch(batch []acctJob) error {
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := s.Q.WithTx(tx)
	for _, j := range batch {
		if err := j.write(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
