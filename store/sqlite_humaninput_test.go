package store_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/store"
)

// HumanInputStore tests focus on the per-row write/read fidelity that
// keeps the multi-select feature honest end-to-end. The migration
// + scan plumbing for `multi_select INTEGER NOT NULL DEFAULT 0` is
// dialect-specific (sqlite uses int 0/1, postgres uses real bool), so
// a column-type mistake on either side will surface as a scan or
// unmarshal error here.

var _ = Describe("HumanInputStore (SQLite)", func() {
	var (
		bundle  *store.Bundle
		cleanup func()
	)

	BeforeEach(func() {
		bundle, cleanup = newSQLiteBundle()
	})
	AfterEach(func() { cleanup() })

	Describe("CreateRequest + GetByToolCallID round trip", func() {
		It("preserves every persisted field including multi_select=true", func() {
			rec := &store.HumanInputRequestRecord{
				MissionID:         "m-1",
				TaskID:            "t-1",
				ToolCallID:        "tc-multi",
				Question:          "Pick any that apply",
				ShortSummary:      "Pick any",
				AdditionalContext: "background _markdown_",
				Choices:           []string{"A", "B", "C"},
				MultiSelect:       true,
			}
			Expect(bundle.HumanInputs.CreateRequest(rec)).To(Succeed())

			got, err := bundle.HumanInputs.GetByToolCallID("tc-multi")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.MultiSelect).To(BeTrue(),
				"multi_select column must round-trip — false here would mean the migration / scan is broken")
			Expect(got.Choices).To(Equal([]string{"A", "B", "C"}))
			Expect(got.MissionID).To(Equal("m-1"))
			Expect(got.TaskID).To(Equal("t-1"))
			Expect(got.ShortSummary).To(Equal("Pick any"))
			Expect(got.AdditionalContext).To(Equal("background _markdown_"))
			Expect(got.State).To(Equal(store.HumanInputStateOpen))
		})

		It("defaults multi_select to false when not set on the record", func() {
			rec := &store.HumanInputRequestRecord{
				ToolCallID: "tc-single",
				Question:   "Pick one",
				Choices:    []string{"A", "B"},
				// MultiSelect not set
			}
			Expect(bundle.HumanInputs.CreateRequest(rec)).To(Succeed())

			got, err := bundle.HumanInputs.GetByToolCallID("tc-single")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.MultiSelect).To(BeFalse(),
				"the schema's NOT NULL DEFAULT 0 must produce false when omitted on insert")
		})

		It("is idempotent on tool_call_id (squadron crash + re-request must not duplicate)", func() {
			rec := &store.HumanInputRequestRecord{
				ToolCallID: "tc-1", Question: "?",
			}
			Expect(bundle.HumanInputs.CreateRequest(rec)).To(Succeed())

			// Second insert with the same tool_call_id is a no-op (ON CONFLICT DO NOTHING).
			Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				ToolCallID: "tc-1", Question: "different question",
			})).To(Succeed())

			rows, total, err := bundle.HumanInputs.ListRequests(store.HumanInputFilter{})
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(1))
			Expect(rows).To(HaveLen(1))
			Expect(rows[0].Question).To(Equal("?"),
				"the original row's content must survive a re-insert")
		})
	})

	Describe("ResolveRequest", func() {
		It("transitions open → resolved and persists the response + responder", func() {
			rec := &store.HumanInputRequestRecord{
				ToolCallID: "tc-1", Question: "?",
			}
			Expect(bundle.HumanInputs.CreateRequest(rec)).To(Succeed())

			resolved, err := bundle.HumanInputs.ResolveRequest("tc-1", "yes", "user@example.com")
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved.State).To(Equal(store.HumanInputStateResolved))
			Expect(resolved.Response).NotTo(BeNil())
			Expect(*resolved.Response).To(Equal("yes"))
			Expect(*resolved.ResponderUserID).To(Equal("user@example.com"))
			Expect(resolved.ResolvedAt).NotTo(BeNil())
			Expect(resolved.ResolvedAt.IsZero()).To(BeFalse())
		})

		It("does not overwrite an already-resolved row (caller treats this as AlreadyResolved)", func() {
			rec := &store.HumanInputRequestRecord{ToolCallID: "tc-1", Question: "?"}
			Expect(bundle.HumanInputs.CreateRequest(rec)).To(Succeed())

			_, err := bundle.HumanInputs.ResolveRequest("tc-1", "first", "alice")
			Expect(err).NotTo(HaveOccurred())

			second, err := bundle.HumanInputs.ResolveRequest("tc-1", "second", "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(*second.Response).To(Equal("first"),
				"already-resolved rows must keep their original answer — the UPDATE's WHERE state=open clause guards this")
			Expect(*second.ResponderUserID).To(Equal("alice"))
		})
	})

	Describe("ListRequests filters", func() {
		BeforeEach(func() {
			oldT := time.Now().Add(-2 * time.Hour)
			newT := time.Now()
			Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				ToolCallID: "open-m1-old", MissionID: "m1", Question: "?", RequestedAt: oldT,
			})).To(Succeed())
			Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				ToolCallID: "open-m1-new", MissionID: "m1", Question: "?", RequestedAt: newT,
			})).To(Succeed())
			Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				ToolCallID: "open-m2", MissionID: "m2", Question: "?",
			})).To(Succeed())
			Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				ToolCallID: "resolved-m1", MissionID: "m1", Question: "?",
			})).To(Succeed())
			_, err := bundle.HumanInputs.ResolveRequest("resolved-m1", "x", "u")
			Expect(err).NotTo(HaveOccurred())
		})

		It("filters by state", func() {
			rows, total, err := bundle.HumanInputs.ListRequests(store.HumanInputFilter{State: store.HumanInputStateOpen})
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(3))
			Expect(rows).To(HaveLen(3))
			for _, r := range rows {
				Expect(r.State).To(Equal(store.HumanInputStateOpen))
			}
		})

		It("filters by mission_id combined with state", func() {
			rows, total, err := bundle.HumanInputs.ListRequests(store.HumanInputFilter{
				State: store.HumanInputStateOpen, MissionID: "m1",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(2))
			Expect(rows).To(HaveLen(2))
		})

		It("orders newest-first by default and oldest-first when requested", func() {
			rows, _, err := bundle.HumanInputs.ListRequests(store.HumanInputFilter{
				State: store.HumanInputStateOpen, MissionID: "m1",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(rows[0].ToolCallID).To(Equal("open-m1-new"), "default order is newest-first")

			rows, _, err = bundle.HumanInputs.ListRequests(store.HumanInputFilter{
				State: store.HumanInputStateOpen, MissionID: "m1", OldestFirst: true,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(rows[0].ToolCallID).To(Equal("open-m1-old"))
		})

		It("paginates with Limit + Offset", func() {
			// 3 open rows total; ask for page size 2.
			rows, total, err := bundle.HumanInputs.ListRequests(store.HumanInputFilter{
				State: store.HumanInputStateOpen, Limit: 2,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(3), "total ignores limit/offset")
			Expect(rows).To(HaveLen(2))

			rows, _, err = bundle.HumanInputs.ListRequests(store.HumanInputFilter{
				State: store.HumanInputStateOpen, Limit: 2, Offset: 2,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(rows).To(HaveLen(1), "second page has the remaining row")
		})
	})
})
