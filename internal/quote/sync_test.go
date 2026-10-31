package quote

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"oracle/internal/b3"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFilesUntil(t *testing.T) {
	day := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

	got := filesUntil(2024, day)
	want := []string{
		"COTAHIST_A2024.ZIP",
		"COTAHIST_A2025.ZIP",
		"COTAHIST_M012026.ZIP",
		"COTAHIST_M022026.ZIP",
		"COTAHIST_D02032026.ZIP",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}

	if got := filesUntil(2027, day); len(got) != 0 {
		t.Errorf("future year: got %v", got)
	}
}

func TestFilesUntilCalendar(t *testing.T) {
	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name  string
		since int
		day   time.Time
		want  []string
	}{
		{"before the first year B3 publishes", 1900, date(1986, 1, 1), []string{"COTAHIST_D01011986.ZIP"}},
		{"january has no monthly files", 2026, date(2026, 1, 5), []string{"COTAHIST_D01012026.ZIP", "COTAHIST_D02012026.ZIP", "COTAHIST_D05012026.ZIP"}},
		{"month starting on a weekend", 2026, date(2026, 11, 2), append(months(2026, 10), "COTAHIST_D02112026.ZIP")},
		{"today on a weekend", 2026, date(2026, 10, 31), append(months(2026, 9), days(2026, time.October, 1, 2, 5, 6, 7, 8, 9, 12, 13, 14, 15, 16, 19, 20, 21, 22, 23, 26, 27, 28, 29, 30)...)},
		{"first day of the year on a weekend", 2028, date(2028, 1, 1), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filesUntil(tt.since, tt.day); !slices.Equal(got, tt.want) {
				t.Errorf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestFilesUntilStartsAtFirstYear(t *testing.T) {
	got := filesUntil(0, time.Date(1988, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(got) < 2 || got[0] != "COTAHIST_A1986.ZIP" || got[1] != "COTAHIST_A1987.ZIP" {
		t.Errorf("got %v", got)
	}
}

// months lists the monthly files from January to the given month.
func months(year int, until time.Month) []string {
	var files []string
	for m := time.January; m <= until; m++ {
		files = append(files, b3.MonthlyFile(year, m))
	}
	return files
}

func days(year int, month time.Month, list ...int) []string {
	files := make([]string, len(list))
	for i, d := range list {
		files[i] = b3.DailyFile(time.Date(year, month, d, 0, 0, 0, 0, time.UTC))
	}
	return files
}

func TestToday(t *testing.T) {
	got := today()
	if got.Location() != time.UTC || got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
		t.Errorf("got %v, want midnight UTC", got)
	}
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("time zone database not available")
	}
	now := time.Now().In(loc)
	if y, m, d := now.Date(); got.Year() != y || got.Month() != m || got.Day() != d {
		t.Errorf("got %v, want the date in São Paulo %v", got, now)
	}
}

// cogn3 is one round-lot record of a B3 COTAHIST file.
const cogn3 = "012026100202COGN3       010COGNA ON    ON      NM   R$  000000000023800000000002450000000000235000000000024000000000002430000000000243000000000024429896000000000044274400000000010626183600000000000000009999123100000010000000000000BRCOGNACNOR2112"

// fixedClock makes today 2026-01-02, a Friday, so a sync since 2026 needs two daily files.
func fixedClock(t *testing.T) {
	t.Helper()
	orig := now
	now = func() time.Time { return time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = orig })
}

const (
	jan1 = "COTAHIST_D01012026.ZIP"
	jan2 = "COTAHIST_D02012026.ZIP"
)

type response struct {
	status int
	body   []byte
}

// fakeB3 serves the given files and fails the test on any other request.
// It returns the client and the files requested, in order.
func fakeB3(t *testing.T, files map[string]response) (*b3.Client, *[]string) {
	t.Helper()
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		requested = append(requested, name)
		resp, ok := files[name]
		if !ok {
			t.Errorf("unexpected request for %s", name)
			resp.status = http.StatusInternalServerError
		}
		w.WriteHeader(resp.status)
		_, _ = w.Write(resp.body)
	}))
	t.Cleanup(srv.Close)
	return &b3.Client{HTTP: srv.Client(), BaseURL: srv.URL}, &requested
}

// archive is a published COTAHIST file with the given records.
func archive(t *testing.T, records ...string) response {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("COTAHIST.TXT")
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		_, _ = w.Write([]byte(rec + "\n"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return response{http.StatusOK, buf.Bytes()}
}

var notPublished = response{status: http.StatusNotFound}

const (
	insertQuotes = `INSERT INTO "quotes" ("ticker","date","name","spec","isin","open","high","low","average","close","trades","quantity","volume") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	insertSynced = `INSERT INTO "synced_files" ("name","quotes","synced_at") VALUES ($1,$2,$3)`
	tableExists  = `SELECT count(*) FROM information_schema.tables WHERE table_schema = CURRENT_SCHEMA() AND table_name = $1`
)

// expectMigrate expects the tables to be created from scratch.
func expectMigrate(mock sqlmock.Sqlmock) {
	missing := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"count"}).AddRow(0) }
	mock.ExpectQuery(sql(tableExists)).WithArgs("quotes", "BASE TABLE").WillReturnRows(missing())
	mock.ExpectExec(sql(`CREATE TABLE "quotes"`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(sql(`CREATE INDEX IF NOT EXISTS "idx_quotes_date" ON "quotes" ("date")`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(sql(tableExists)).WithArgs("synced_files", "BASE TABLE").WillReturnRows(missing())
	mock.ExpectExec(sql(`CREATE TABLE "synced_files"`)).WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectSynced(mock sqlmock.Sqlmock, names ...string) {
	rows := sqlmock.NewRows([]string{"name"})
	for _, n := range names {
		rows.AddRow(n)
	}
	mock.ExpectQuery(sql(`SELECT "name" FROM "synced_files"`)).WillReturnRows(rows)
}

// expectFile expects one file loaded in its own transaction, with the quotes inserted in the given batch sizes.
func expectFile(mock sqlmock.Sqlmock, name string, batches ...int) {
	mock.ExpectBegin()
	total := 0
	for _, n := range batches {
		mock.ExpectExec(sql(insertQuotes) + ".* ON CONFLICT DO NOTHING$").WillReturnResult(sqlmock.NewResult(0, int64(n)))
		total += n
	}
	mock.ExpectExec(sql(insertSynced)).WithArgs(name, total, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestSyncHistory(t *testing.T) {
	fixedClock(t)
	db, mock := newMock(t)
	client, requested := fakeB3(t, map[string]response{jan1: notPublished, jan2: archive(t, cogn3)})

	expectMigrate(mock)
	expectSynced(mock)
	// Holiday: the transaction is opened and rolled back, nothing is recorded.
	mock.ExpectBegin()
	mock.ExpectRollback()
	// The quote is stored field by field as B3 published it.
	mock.ExpectBegin()
	mock.ExpectExec(sql(insertQuotes)).
		WithArgs("COGN3", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "COGNA ON", "ON      NM", "BRCOGNACNOR2",
			2.38, 2.45, 2.35, 2.40, 2.43, int64(29896), int64(44274400), 106261836.0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(sql(insertSynced)).WithArgs(jan2, 1, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := SyncHistory(t.Context(), db, client, 2026); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*requested, []string{jan1, jan2}) {
		t.Errorf("requested %v", *requested)
	}
}

func TestSyncHistorySkipsSyncedFiles(t *testing.T) {
	fixedClock(t)
	db, mock := newMock(t)
	client, requested := fakeB3(t, map[string]response{jan2: archive(t, cogn3)})

	expectMigrate(mock)
	expectSynced(mock, jan1)
	expectFile(mock, jan2, 1)

	if err := SyncHistory(t.Context(), db, client, 2026); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*requested, []string{jan2}) {
		t.Errorf("requested %v, want only the missing file", *requested)
	}
}

func TestSyncHistoryUpToDate(t *testing.T) {
	fixedClock(t)
	db, mock := newMock(t)
	client, requested := fakeB3(t, nil)

	expectMigrate(mock)
	expectSynced(mock, jan1, jan2)

	if err := SyncHistory(t.Context(), db, client, 2026); err != nil {
		t.Fatal(err)
	}
	if len(*requested) != 0 {
		t.Errorf("requested %v, want nothing", *requested)
	}
}

func TestSyncHistoryInsertsInBatches(t *testing.T) {
	fixedClock(t)
	db, mock := newMock(t)
	tests := map[string]struct {
		records int
		batches []int
	}{
		jan1: {0, nil},
		jan2: {batchSize + 1, []int{batchSize, 1}},
	}
	records := func(n int) []string {
		recs := make([]string, n)
		for i := range recs {
			recs[i] = cogn3
		}
		return recs
	}
	client, _ := fakeB3(t, map[string]response{
		jan1: archive(t, records(tests[jan1].records)...),
		jan2: archive(t, records(tests[jan2].records)...),
	})

	expectMigrate(mock)
	expectSynced(mock)
	expectFile(mock, jan1, tests[jan1].batches...) // an empty file is still recorded, so it is not downloaded again
	expectFile(mock, jan2, tests[jan2].batches...)

	if err := SyncHistory(t.Context(), db, client, 2026); err != nil {
		t.Fatal(err)
	}
}

func TestSyncHistoryErrors(t *testing.T) {
	fail := errors.New("database down")
	published := func(t *testing.T) map[string]response {
		t.Helper()
		return map[string]response{jan1: archive(t, cogn3)}
	}
	tests := []struct {
		name   string
		files  func(*testing.T) map[string]response
		expect func(sqlmock.Sqlmock)
		want   string
	}{
		{
			name:  "migration fails",
			files: func(*testing.T) map[string]response { return nil },
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(sql(tableExists)).WillReturnError(fail)
			},
			want: "migrate",
		},
		{
			name:  "synced files cannot be listed",
			files: func(*testing.T) map[string]response { return nil },
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				mock.ExpectQuery(sql(`SELECT "name" FROM "synced_files"`)).WillReturnError(fail)
			},
		},
		{
			name: "B3 fails",
			files: func(*testing.T) map[string]response {
				return map[string]response{jan1: {status: http.StatusBadGateway}}
			},
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin()
				mock.ExpectRollback()
			},
			want: "sync " + jan1,
		},
		{
			name: "corrupt file",
			files: func(t *testing.T) map[string]response {
				t.Helper()
				return map[string]response{jan1: archive(t, cogn3[:2]+"2026AB02"+cogn3[10:])}
			},
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin()
				mock.ExpectRollback()
			},
			want: "sync " + jan1,
		},
		{
			name:  "begin fails",
			files: published,
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin().WillReturnError(fail)
			},
			want: "sync " + jan1,
		},
		{
			name:  "quotes cannot be inserted",
			files: published,
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin()
				mock.ExpectExec(sql(insertQuotes)).WillReturnError(fail)
				mock.ExpectRollback()
			},
			want: "sync " + jan1,
		},
		{
			name: "a full batch cannot be inserted",
			files: func(t *testing.T) map[string]response {
				t.Helper()
				recs := make([]string, batchSize)
				for i := range recs {
					recs[i] = cogn3
				}
				return map[string]response{jan1: archive(t, recs...)}
			},
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin()
				mock.ExpectExec(sql(insertQuotes)).WillReturnError(fail)
				mock.ExpectRollback()
			},
			want: "sync " + jan1,
		},
		{
			name:  "file cannot be recorded as synced",
			files: published,
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin()
				mock.ExpectExec(sql(insertQuotes)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(sql(insertSynced)).WillReturnError(fail)
				mock.ExpectRollback()
			},
			want: "sync " + jan1,
		},
		{
			name:  "commit fails",
			files: published,
			expect: func(mock sqlmock.Sqlmock) {
				expectMigrate(mock)
				expectSynced(mock)
				mock.ExpectBegin()
				mock.ExpectExec(sql(insertQuotes)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(sql(insertSynced)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit().WillReturnError(fail)
			},
			want: "sync " + jan1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixedClock(t)
			db, mock := newMock(t)
			client, requested := fakeB3(t, tt.files(t))
			tt.expect(mock)

			err := SyncHistory(t.Context(), db, client, 2026)
			if err == nil {
				t.Fatal("got no error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %q, want it to mention %q", err, tt.want)
			}
			// The sync stops at the first failure: the second file is never downloaded.
			if slices.Contains(*requested, jan2) {
				t.Errorf("requested %v after a failure", *requested)
			}
		})
	}
}

func TestTodayUsesSaoPaulo(t *testing.T) {
	orig := now
	t.Cleanup(func() { now = orig })
	// 01:00 UTC is still the previous day in São Paulo (UTC-3).
	now = func() time.Time { return time.Date(2026, 3, 3, 1, 0, 0, 0, time.UTC) }
	if got, want := today(), time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
