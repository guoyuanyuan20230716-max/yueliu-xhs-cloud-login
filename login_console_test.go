package main

import (
 "net/http/httptest"
 "strings"
 "testing"

 "github.com/gin-gonic/gin"
)

func TestConsoleOrigin(t *testing.T) {
 for _,u:=range []string{"https://www.xiaohongshu.com/explore","https://xiaohongshu.com/","https://edith.xiaohongshu.com/"} { if !consoleOriginAllowed(u) {t.Errorf("rejected %s",u)} }
 for _,u:=range []string{"https://evilxiaohongshu.com/","https://xiaohongshu.com.evil.test/","http://www.xiaohongshu.com/","file:///etc/passwd","about:blank","https://xiaohongshu.com@evil.test/"} {if consoleOriginAllowed(u){t.Errorf("accepted %s",u)}}
}

func TestConsoleAuthAndSessions(t *testing.T) {
 gin.SetMode(gin.TestMode)
 for _,tc:=range []struct{name,token,auth,origin,session,path,method string; want int}{
  {name:"fail closed",path:"/api/v1/login-console/start",method:"POST",want:503},
  {name:"missing bearer",token:"test-secret",path:"/api/v1/login-console/start",method:"POST",want:401},
  {name:"wrong bearer",token:"test-secret",auth:"Bearer wrong",path:"/api/v1/login-console/start",method:"POST",want:401},
  {name:"cross origin",token:"test-secret",auth:"Bearer test-secret",origin:"https://evil.test",path:"/api/v1/login-console/start",method:"POST",want:403},
  {name:"no session",token:"test-secret",auth:"Bearer test-secret",path:"/api/v1/login-console/frame",method:"GET",want:409},
  {name:"stale session",token:"test-secret",auth:"Bearer test-secret",session:"old",path:"/api/v1/login-console/action",method:"POST",want:409},
 } {t.Run(tc.name,func(t *testing.T){
   r:=gin.New();registerLoginConsole(r,&AppServer{authToken:tc.token})
   req:=httptest.NewRequest(tc.method,tc.path,strings.NewReader(`{}`));req.Header.Set("Authorization",tc.auth);req.Header.Set("Origin",tc.origin);req.Header.Set("X-Login-Session",tc.session)
   w:=httptest.NewRecorder();r.ServeHTTP(w,req)
   if w.Code!=tc.want {t.Fatalf("got %d want %d: %s",w.Code,tc.want,w.Body.String())}
   if w.Header().Get("Cache-Control")!="no-store" {t.Error("sensitive response is cacheable")}
  })}
}
