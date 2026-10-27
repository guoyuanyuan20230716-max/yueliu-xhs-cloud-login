package main

import (
 "context"
 _ "embed"
 "encoding/json"
 "fmt"
 "net/url"
 "os"
 "path/filepath"
 "sync"
 "time"

 "github.com/gin-gonic/gin"
 "github.com/xpzouying/xiaohongshu-mcp/cookies"
 "github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

//go:embed reader.html
var readerHTML []byte

type readerItem struct {
 ID string `json:"id"`
 Title string `json:"title"`
 Author string `json:"author"`
 URL string `json:"url"`
 SeenAt time.Time `json:"seen_at"`
}
type readerState struct {
 Items []readerItem `json:"items"`
 UpdatedAt time.Time `json:"updated_at"`
 AttemptAt time.Time `json:"attempt_at"`
 Error string `json:"error"`
 Running bool `json:"running"`
 Automatic bool `json:"automatic"`
}
type cloudReader struct {
 mu sync.Mutex
 state readerState
 path string
 console *loginConsole
}

func registerReader(router *gin.Engine, app *AppServer, console *loginConsole) {
 r := &cloudReader{console:console,path:filepath.Join(filepath.Dir(cookies.GetCookiesFilePath()),"recommendations.json")}
 if data,err:=os.ReadFile(r.path); err==nil { _=json.Unmarshal(data,&r.state) }
 r.state.Running=false
 r.state.Automatic=os.Getenv("SYNC_ENABLED")=="true"
 router.GET("/",func(c *gin.Context){c.Header("Cache-Control","no-store");c.Data(200,"text/html; charset=utf-8",readerHTML)})
 api:=router.Group("/api/v1/reader")
 api.Use(func(c *gin.Context){
  c.Header("Cache-Control","no-store")
  c.Writer.Header().Del("Access-Control-Allow-Origin")
  if app.authToken=="" {c.AbortWithStatus(503);return}
  if origin:=c.GetHeader("Origin");origin!="" {u,err:=url.Parse(origin);if err!=nil || u.Host!=c.Request.Host || (u.Scheme!="https" && u.Scheme!="http"){c.AbortWithStatus(403);return}}
 },authMiddleware(app.authToken))
 api.GET("",func(c *gin.Context){r.mu.Lock();defer r.mu.Unlock();c.JSON(200,r.state)})
 api.POST("/sync",func(c *gin.Context){if !r.start(){c.JSON(409,gin.H{"error":"同步或登录进行中，或距上次同步不足五分钟"});return};c.JSON(202,gin.H{"ok":true})})
 if r.state.Automatic {go func(){r.start();ticker:=time.NewTicker(time.Hour);defer ticker.Stop();for range ticker.C {r.start()}}()}
}

func (r *cloudReader) start() bool {
 r.mu.Lock()
 if r.state.Running || time.Since(r.state.AttemptAt)<5*time.Minute {r.mu.Unlock();return false}
 if !r.console.mu.TryLock(){r.mu.Unlock();return false}
 if r.console.page!=nil || r.console.status=="starting" {r.console.mu.Unlock();r.mu.Unlock();return false}
 r.state.Running=true;r.state.AttemptAt=time.Now().UTC();r.state.Error=""
 r.mu.Unlock()
 go func(){
  defer r.console.mu.Unlock()
  defer func(){if recover()!=nil {r.mu.Lock();r.state.Running=false;r.state.Error="云端浏览器异常，请稍后重试";r.mu.Unlock()}}()
  items,err:=fetchReaderItems()
  r.mu.Lock();defer r.mu.Unlock();r.state.Running=false
  if err!=nil {r.state.Error=err.Error();return}
  merged:=mergeReaderItems(items,r.state.Items)
  next:=r.state;next.Items=merged;next.UpdatedAt=time.Now().UTC()
  data,err:=json.Marshal(next)
  if err==nil {err=writeReaderCache(r.path,data)}
  if err!=nil {r.state.Error="读取成功，但云端缓存保存失败";return}
  r.state=next
 }()
 return true
}

func writeReaderCache(path string,data []byte) error {
 if err:=os.MkdirAll(filepath.Dir(path),0700);err!=nil{return err}
 f,err:=os.CreateTemp(filepath.Dir(path),".recommendations-*");if err!=nil{return err}
 tmp:=f.Name();defer os.Remove(tmp)
 if _,err=f.Write(data);err!=nil{f.Close();return err}
 if err=f.Sync();err!=nil{f.Close();return err};if err=f.Close();err!=nil{return err}
 return os.Rename(tmp,path)
}

func mergeReaderItems(fresh,old []readerItem) []readerItem {
 result:=make([]readerItem,0,300);seen:=map[string]bool{}
 for _,batch:=range [][]readerItem{fresh,old}{for _,item:=range batch{if item.ID==""||seen[item.ID]{continue};seen[item.ID]=true;result=append(result,item);if len(result)==300{return result}}}
 return result
}

func fetchReaderItems() ([]readerItem,error) {
 if _,err:=os.Stat(cookies.GetCookiesFilePath());err!=nil{return nil,fmt.Errorf("请先完成云端登录")}
 b:=newBrowser();defer b.Close()
 ctx,cancel:=context.WithTimeout(context.Background(),150*time.Second);defer cancel()
 p:=b.NewPage().Context(ctx)
 if err:=p.Navigate("https://www.xiaohongshu.com/explore");err!=nil{return nil,fmt.Errorf("小红书页面加载失败")}
 // Poll the hydrated feed, without waiting for ads, videos or the whole DOM to settle.
 for {
  result,err:=p.Eval(`() => {
   const s=window.__INITIAL_STATE__;const u=s?.user?.userInfo;const user=u?.value??u?._value??u;
   const f=s?.feed?.feeds;const feeds=f?.value??f?._value??f;
   return JSON.stringify({logged:!!(user && !user.guest && (user.userId||user.user_id)),feeds:Array.isArray(feeds)?feeds:[]});
  }`)
  if err!=nil{return nil,fmt.Errorf("读取推荐超时，请稍后重试")}
  var payload struct{Logged bool `json:"logged"`;Feeds []xiaohongshu.Feed `json:"feeds"`}
  if json.Unmarshal([]byte(result.Value.Str()),&payload)==nil && payload.Logged && len(payload.Feeds)>0 {
   items:=[]readerItem{}
   for _,f:=range payload.Feeds {if f.ModelType!="note"||f.ID==""{continue};author:=f.NoteCard.User.Nickname;if author==""{author=f.NoteCard.User.NickName};title:=f.NoteCard.DisplayTitle;if title==""{title="查看这条推荐"};q:=url.Values{"xsec_token":{f.XsecToken},"xsec_source":{"pc_feed"}};items=append(items,readerItem{ID:f.ID,Title:title,Author:author,URL:"https://www.xiaohongshu.com/explore/"+url.PathEscape(f.ID)+"?"+q.Encode(),SeenAt:time.Now().UTC()})}
   if len(items)>0 {if err:=saveCookies(p);err!=nil{return nil,fmt.Errorf("登录状态续存失败")};return items,nil}
  }
  select {case <-ctx.Done():return nil,fmt.Errorf("未读到已登录的推荐，请检查云端登录或平台验证");case <-time.After(2*time.Second):}
 }
}
