package main

import (
 "fmt"
 "os"
 "path/filepath"
 "testing"
)

func TestConsoleReaderMerge(t *testing.T){
 old:=[]readerItem{{ID:"a",Title:"old"},{ID:"b"}}
 fresh:=[]readerItem{{ID:"a",Title:"new"},{ID:"a"},{ID:""}}
 got:=mergeReaderItems(fresh,old)
 if len(got)!=2||got[0].Title!="new"||got[1].ID!="b"{t.Fatalf("unexpected merge: %#v",got)}
 many:=[]readerItem{};for i:=0;i<400;i++{many=append(many,readerItem{ID:fmt.Sprint(i)})}
 if len(mergeReaderItems(many,nil))!=300{t.Fatal("cache cap failed")}
}
func TestConsoleReaderCache(t *testing.T){
 path:=filepath.Join(t.TempDir(),"data","recommendations.json")
 for _,data:=range []string{`{"items":[]}`,`{"items":[{"id":"a"}]}`} {if err:=writeReaderCache(path,[]byte(data));err!=nil{t.Fatal(err)};got,err:=os.ReadFile(path);if err!=nil||string(got)!=data{t.Fatal("cache mismatch")}}
 entries,_:=os.ReadDir(filepath.Dir(path));if len(entries)!=1{t.Fatal("temporary file left behind")}
}
