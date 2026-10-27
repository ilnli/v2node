package audit

import (
 "os"
 "path/filepath"
 "testing"
)

func sampleBatch(id string) Batch {
 return Batch{ID:id, Target:"node-a", Records:[]Record{{UserID:7, NodeID:3, Timestamp:1700000000000, Destination:"203.0.113.8", Port:443, Network:"tcp", Domain:"example.com"}}}
}

func TestSpillDropNewestAckAndRecover(t *testing.T) {
 dir:=t.TempDir()
 // One tiny batch in memory, one on disk; reserve two filesystem blocks per batch.
 q,err:=Open(Options{Directory:dir,MemoryBytes:400,DiskBytes:16384})
 if err!=nil {t.Fatal(err)}
 if !q.Add(sampleBatch("first")) || !q.Add(sampleBatch("second")) {t.Fatal("old batches rejected")}
 if q.Stats().MemoryBytes==0 || q.Stats().DiskBytes==0 {t.Fatalf("did not spill: %+v",q.Stats())}
 if q.Add(sampleBatch("newest")) {t.Fatal("accepted newest above capacity")}
 b,ok:=q.Peek(); if !ok || b.ID!="first" {t.Fatal("oldest lost",b)}
 if err=q.Ack("wrong"); err==nil {t.Fatal("wrong acknowledgment removed head")}
 if err=q.Close(); err!=nil {t.Fatal(err)}
 q,err=Open(Options{Directory:dir,MemoryBytes:400,DiskBytes:16384}); if err!=nil {t.Fatal(err)}
 defer q.Close()
 for _,id:=range []string{"first","second"} {
  b,ok=q.Peek(); if !ok || b.ID!=id {t.Fatalf("recovery ordering: got %+v want %s",b,id)}
  if err=q.Ack(id);err!=nil {t.Fatal(err)}
 }
 if q.Stats().DiskBytes!=0 || q.Stats().Batches!=0 {t.Fatal("acknowledged data retained")}
 files,_:=filepath.Glob(filepath.Join(dir,"*.batch")); if len(files)!=0 {t.Fatal("spool not cleaned",files)}
 if !q.Add(sampleBatch("after")) || q.Stats().DiskBytes!=0 {t.Fatal("did not return to memory")}
}

func TestFailedDiskWritePreservesOldRecords(t *testing.T) {
 dir:=t.TempDir()
 q,err:=Open(Options{Directory:dir,MemoryBytes:1,DiskBytes:16384});if err!=nil{t.Fatal(err)}
 if !q.Add(sampleBatch("old")){t.Fatal("add")}
 // Make the next batch filename unwritable without relying on Unix permissions as root.
 if err=os.Mkdir(filepath.Join(dir,"00000000000000000002.batch.tmp"),0700);err!=nil{t.Fatal(err)}
 if q.Add(sampleBatch("new")){t.Fatal("disk failure accepted")}
 b,ok:=q.Peek();if !ok||b.ID!="old"{t.Fatal("disk failure lost old data")}
 if err=q.Ack("old");err!=nil{t.Fatal(err)}
}

func TestLimitsRejected(t *testing.T) {
 for _,o:=range []Options{{MemoryBytes:256<<20,DiskBytes:1<<30},{MemoryBytes:1,DiskBytes:(1<<30)+1}} {
  o.Directory=t.TempDir();if q,err:=Open(o);err==nil {q.Close();t.Fatal("unsafe limit accepted")}
 }
}
