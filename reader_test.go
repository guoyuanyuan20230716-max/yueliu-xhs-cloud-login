package main

import (
 "fmt"
 "os"
 "net/http/httptest"
 "path/filepath"
 "testing"
 "time"
 "github.com/gin-gonic/gin"
)

func TestConsoleReaderMerge(t *testing.T){
 old:=[]readerItem{{ID:"a",Title:"old"},{ID:"b"}}
 fresh:=[]readerItem{{ID:"a",Title:"new"},{ID:"a"},{ID:""}}
 got:=mergeReaderItems(fresh,old)
 if len(got)!=2||got[0].Title!="new"||got[1].ID!="b"{t.Fatalf("unexpected merge: %#v",got)}
 many:=[]readerItem{};for i:=0;i<400;i++{many=append(many,readerItem{ID:fmt.Sprint(i)})}
 if len(mergeReaderItems(many,nil))!=300{t.Fatal("cache cap failed")}
}

func TestConsoleReaderAccess(t *testing.T){
 t.Setenv("SYNC_ENABLED","false");t.Setenv("COOKIES_PATH",filepath.Join(t.TempDir(),"cookies.json"))
 for _,tc:=range []struct{token,header,origin string;want int}{{"","","",503},{"secret","","",401},{"secret","Bearer secret","https://evil.example",403},{"secret","Bearer secret","https://reader.example",200}}{
  router:=gin.New();registerReader(router,&AppServer{authToken:tc.token},&loginConsole{})
  req:=httptest.NewRequest("GET","https://reader.example/api/v1/reader",nil);req.Header.Set("Authorization",tc.header);req.Header.Set("Origin",tc.origin)
  res:=httptest.NewRecorder();router.ServeHTTP(res,req)
  if res.Code!=tc.want{t.Fatalf("status %d want %d",res.Code,tc.want)}
  if res.Header().Get("Cache-Control")!="no-store"{t.Fatal("private cache headers missing")}
 }
}

func TestConsoleReaderSkipsActiveLogin(t *testing.T){
 r:=&cloudReader{console:&loginConsole{status:"starting"}}
 if r.start()||r.state.Running{t.Fatal("sync must not overlap login")}
 r.console.status="saved";r.state.AttemptAt=time.Now()
 if r.start(){t.Fatal("sync must honor minimum interval")}
}
func TestConsoleReaderCache(t *testing.T){
 path:=filepath.Join(t.TempDir(),"data","recommendations.json")
 for _,data:=range []string{`{"items":[]}`,`{"items":[{"id":"a"}]}`} {if err:=writeReaderCache(path,[]byte(data));err!=nil{t.Fatal(err)};got,err:=os.ReadFile(path);if err!=nil||string(got)!=data{t.Fatal("cache mismatch")}}
 entries,_:=os.ReadDir(filepath.Dir(path));if len(entries)!=1{t.Fatal("temporary file left behind")}
}
