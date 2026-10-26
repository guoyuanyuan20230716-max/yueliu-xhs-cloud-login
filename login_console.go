package main

import (
 "crypto/rand"
 "encoding/hex"
 _ "embed"
 "fmt"
 "math"
 "net/http"
 "net/url"
 "strings"
 "sync"
 "time"

 "github.com/gin-gonic/gin"
 "github.com/go-rod/rod"
 "github.com/go-rod/rod/lib/input"
 "github.com/go-rod/rod/lib/proto"
)

//go:embed login_console.html
var loginConsoleHTML string

const consoleWidth = 1100
const consoleHeight = 820
const consoleTTL = 10 * time.Minute

type loginConsole struct {
 mu sync.Mutex
 id string
 status string
 note string
 expires time.Time
 page *rod.Page
 closeBrowser func()
}

func consoleOriginAllowed(raw string) bool {
 u, err := url.Parse(raw)
 return err == nil && u.Scheme == "https" && (u.Hostname() == "xiaohongshu.com" || strings.HasSuffix(u.Hostname(), ".xiaohongshu.com"))
}

func (s *loginConsole) endLocked(status string) {
 if s.closeBrowser != nil { s.closeBrowser(); s.closeBrowser = nil }
 s.page = nil
 s.status = status
}

func (s *loginConsole) stateLocked() gin.H {
 return gin.H{"session":s.id,"status":s.status,"note":s.note,"expires_at":s.expires.UTC().Format(time.RFC3339),"width":consoleWidth,"height":consoleHeight}
}

func consoleError(c *gin.Context, code int, message string) { c.JSON(code, gin.H{"error":message}) }

func registerLoginConsole(router *gin.Engine, app *AppServer) {
 s := &loginConsole{status:"idle"}
 router.GET("/login-console", func(c *gin.Context) {
  c.Header("Cache-Control","no-store")
  c.Header("Referrer-Policy","no-referrer")
  c.Header("X-Frame-Options","DENY")
  c.Header("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
  c.Data(200,"text/html; charset=utf-8",[]byte(loginConsoleHTML))
 })
 api := router.Group("/api/v1/login-console")
 api.Use(func(c *gin.Context) {
  c.Header("Cache-Control","no-store")
  c.Header("X-Content-Type-Options","nosniff")
  c.Writer.Header().Del("Access-Control-Allow-Origin")
  if app.authToken == "" { consoleError(c,503,"必须先配置 AUTH_TOKEN"); c.Abort(); return }
  if origin := c.GetHeader("Origin"); origin != "" {
   u, err := url.Parse(origin)
   if err != nil || u.Host != c.Request.Host || (u.Scheme != "https" && u.Scheme != "http") { consoleError(c,403,"不允许跨站操作"); c.Abort(); return }
  }
 })
 api.Use(authMiddleware(app.authToken))
 api.POST("/start",func(c *gin.Context) {
  if !s.mu.TryLock() { consoleError(c,409,"操作中，请稍后重试"); return }; defer s.mu.Unlock()
  if s.status == "starting" || (s.page != nil && time.Now().Before(s.expires)) { c.JSON(200,s.stateLocked()); return }
  s.endLocked("idle")
  token := make([]byte,16)
  if _,err := rand.Read(token); err != nil { consoleError(c,500,"无法创建会话"); return }
  s.id = hex.EncodeToString(token); s.status = "starting"; s.note = "正在启动云端浏览器"; s.expires = time.Now().Add(consoleTTL)
  id := s.id
  go s.launch(id)
  c.JSON(202,s.stateLocked())
 })
 // Every following operation is bound to the displayed session, never a replacement.
 api.Use(func(c *gin.Context) {
  if !s.mu.TryLock() { consoleError(c,409,"操作中，请稍后重试"); c.Abort(); return }
  defer s.mu.Unlock()
  if c.GetHeader("X-Login-Session") == "" || c.GetHeader("X-Login-Session") != s.id { consoleError(c,409,"会话已变化，请重新连接"); c.Abort(); return }
  if s.status != "starting" && s.page != nil && time.Now().After(s.expires) { s.endLocked("expired") }
  c.Next()
 })
 api.GET("/state",func(c *gin.Context) { c.JSON(200,s.stateLocked()) })
 api.POST("/close",func(c *gin.Context) {
  if s.status == "starting" { consoleError(c,409,"浏览器启动中，请稍后关闭"); return }
  s.endLocked("closed"); s.note = "会话已关闭"; c.JSON(200,s.stateLocked())
 })
 api.Use(func(c *gin.Context) {
  if s.page == nil { consoleError(c,409,"登录会话未就绪或已结束"); c.Abort(); return }
  info,err := s.page.Timeout(8*time.Second).Info()
  if err != nil { consoleError(c,502,"无法读取浏览器状态"); c.Abort(); return }
  if !consoleOriginAllowed(info.URL) { consoleError(c,403,"当前页面不属于小红书，请关闭会话后重试"); c.Abort(); return }
 })
 api.GET("/frame",func(c *gin.Context) {
  data,err := s.page.Timeout(15*time.Second).Screenshot(false,&proto.PageCaptureScreenshot{Format:proto.PageCaptureScreenshotFormatPng})
  if err != nil { consoleError(c,504,"获取画面超时；请手动刷新"); return }
  c.Data(200,"image/png",data)
 })
 api.POST("/action",func(c *gin.Context) {
  c.Request.Body = http.MaxBytesReader(c.Writer,c.Request.Body,4096)
  var req struct { Kind string `json:"kind"`; X float64 `json:"x"`; Y float64 `json:"y"`; Text string `json:"text"`; Key string `json:"key"`; Delta float64 `json:"delta"` }
  if err := c.ShouldBindJSON(&req); err != nil { consoleError(c,400,"无效操作"); return }
  p := s.page.Timeout(12*time.Second)
  var err error
  switch req.Kind {
  case "click":
   if math.IsNaN(req.X) || math.IsNaN(req.Y) || req.X<0 || req.Y<0 || req.X>=consoleWidth || req.Y>=consoleHeight { consoleError(c,400,"点击位置超出画面"); return }
   err = p.Mouse.MoveTo(proto.Point{X:req.X,Y:req.Y}); if err == nil { err = p.Mouse.Click(proto.InputMouseButtonLeft,1) }
  case "text":
   if len(req.Text)==0 || len(req.Text)>128 { consoleError(c,400,"输入内容长度不正确"); return }
   // InsertText enters text into the field the user actually clicked. Never log it.
   err = p.InsertText(req.Text)
  case "key":
   keys := map[string]input.Key{"Enter":input.Enter,"Tab":input.Tab,"Backspace":input.Backspace,"Escape":input.Escape}
   key,ok := keys[req.Key]; if !ok { consoleError(c,400,"不支持的按键"); return }; err = p.Keyboard.Type(key)
  case "scroll":
   if req.Delta != 400 && req.Delta != -400 { consoleError(c,400,"无效滚动距离"); return }; err = p.Mouse.Scroll(0,req.Delta,1)
  default: consoleError(c,400,"不支持的操作"); return
  }
  if err != nil { consoleError(c,504,"操作未确认完成，请刷新画面后检查，勿重复提交验证码"); return }
  c.JSON(200,gin.H{"ok":true})
 })
 api.POST("/save",func(c *gin.Context) {
  // Check THIS page, without navigation or another browser that loses the challenge.
  p := s.page.Timeout(12*time.Second)
  result,err := p.Eval(`() => {
   const u=window.__INITIAL_STATE__?.user;
   const info=u?.userInfo?.value ?? u?.userInfo;
   const el=document.querySelector('.main-container .user .link-wrapper .channel');
   return !!(el && el.getClientRects().length && info && !info.guest && (info.userId || info.user_id));
  }`)
  if err != nil { consoleError(c,504,"登录检查超时，请刷新画面"); return }
  if !result.Value.Bool() { consoleError(c,409,"尚未检测到完整登录，请先完成短信验证并进入小红书主页"); return }
  if err := saveCookies(p); err != nil { consoleError(c,500,"登录已完成，但保存失败，请重试保存"); return }
  s.endLocked("saved"); s.note = "已确认登录并保存到云端"; c.JSON(200,s.stateLocked())
 })
 // Avoid creating additional browsers through the old API during verification.
 router.Use(func(c *gin.Context) {
  if !s.mu.TryLock() { consoleError(c,409,"登录操作进行中，请稍后重试"); c.Abort(); return }
  active := s.status=="starting" || s.page!=nil
  s.mu.Unlock()
  if active { consoleError(c,409,"请先完成或关闭可视化登录会话"); c.Abort(); return }
  c.Next()
 })
}

func (s *loginConsole) launch(id string) {
 var cleanup func()
 transferred := false
 defer func() {
  if r := recover(); r != nil { s.mu.Lock(); if s.id==id { s.status="error"; s.note="云端浏览器启动失败，请检查服务日志或稍后重试" }; s.mu.Unlock() }
  if !transferred && cleanup != nil { cleanup() }
 }()
 b := newBrowser()
 cleanup = func(){ b.Close() }
 page := b.NewPage()
 err := page.Timeout(15*time.Second).SetViewport(&proto.EmulationSetDeviceMetricsOverride{Width:consoleWidth,Height:consoleHeight,DeviceScaleFactor:1,Mobile:false})
 if err == nil { err = page.Timeout(75*time.Second).Navigate("https://www.xiaohongshu.com/explore") }
 s.mu.Lock()
 defer s.mu.Unlock()
 if s.id!=id { return }
 if err != nil { s.status="error"; s.note=fmt.Sprintf("云端页面加载失败（%T）；请结束后重试",err); return }
 s.page=page; s.closeBrowser=cleanup; transferred=true; s.status="ready"; s.note="请查看画面并完成扫码、短信验证"; s.expires=time.Now().Add(consoleTTL)
 time.AfterFunc(consoleTTL,func(){ s.mu.Lock(); defer s.mu.Unlock(); if s.id==id && s.page!=nil { s.endLocked("expired"); s.note="会话已到期，浏览器已关闭" } })
}
