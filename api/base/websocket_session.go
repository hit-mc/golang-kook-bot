package base

import (
	"errors"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
	"github.com/kaiheila/golang-bot/api/helper"
	log "github.com/sirupsen/logrus"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"
)

type WebSocketSession struct {
	*StateSession
	Token       string
	BaseUrl     string
	SessionFile string
	WsConn      *websocket.Conn
	WsWriteLock *sync.Mutex
	//sWSClient
}

type GateWayHttpApiResult struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Url string `json:"url"`
	} `json:"data"`
}

func NewWebSocketSession(token, baseUrl, sessionFile, gateWay string, compressed int) *WebSocketSession {
	s := &WebSocketSession{
		Token: token, BaseUrl: baseUrl, SessionFile: sessionFile}
	if content, err := os.ReadFile(sessionFile); err == nil && len(content) > 0 {
		data := make([]interface{}, 0)
		err := sonic.Unmarshal(content, &data)
		if err != nil {
			if len(data) == 2 {
				s.SessionId = data[0].(string)
				s.MaxSn = data[0].(int64)
			}
		} else {
			log.WithError(err).Error("unmarsal from sessionFile error", sessionFile)
		}

	}
	s.StateSession = NewStateSession(gateWay, compressed)
	s.NetworkProxy = s
	s.WsWriteLock = new(sync.Mutex)
	return s
}

func (ws *WebSocketSession) ReqGateWay() (error, string) {
	client := helper.NewApiHelper("/v3/gateway/index", ws.Token, ws.BaseUrl, "", "")
	client.SetQuery(map[string]string{"compress": strconv.Itoa(ws.Compressed)})
	data, err := client.Get()
	if err != nil {
		log.WithError(err).Error("ReqGateWay")
		return err, ""
	}
	result := &GateWayHttpApiResult{}
	err = sonic.Unmarshal(data, result)
	if err != nil {
		log.WithError(err).Error("ReqGateWay")
		return err, ""
	}
	if result.Code == 0 && len(result.Data.Url) > 0 {
		return nil, result.Data.Url
	}
	log.WithField("result", result).Error("ReqGateWay resultCode is not 0 or Url is empty")
	return errors.New("resultCode is not 0 or Url is empty"), ""

}
func (ws *WebSocketSession) ConnectWebsocket(gateway string) error {
	if ws.WsConn != nil {
		err := ws.WsConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			log.Error(err)
		}
		err = ws.WsConn.Close()
		if err != nil {
			log.Error(err)
		}
		ws.WsConn = nil
		//等3秒让之前的链接被服务器释放
		time.Sleep(time.Duration(3 * time.Second))
	}

	if ws.SessionId != "" {
		gateway += "&" + fmt.Sprintf("sn=%d&sessionId=%s&resume=1", ws.MaxSn, ws.SessionId)
	}
	log.WithField("gateway", gateway).Debug("ConnectWebsocket")
	c, resp, err := websocket.DefaultDialer.Dial(gateway, nil)
	log.Debugf("webscoket dial resp:%+v", resp)
	if err != nil {
		log.WithError(err).Error("ConnectWebsocket Dial")
		return err
	}
	ws.WsConn = c

	ws.wsConnectOk()
	go func() {
		defer func() {
			if c != nil {
				c.Close()
			}
			//ws.StateSession.Reconnect()
		}()
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.WithField("message", message).Trace("websocket recv")
			err, _ = ws.ReceiveData(message)
			if err != nil {
				log.WithError(err).Error("ReceiveData error")
			}
		}
	}()
	return nil
}

func (ws *WebSocketSession) SendData(data []byte) error {
	ws.WsWriteLock.Lock()
	defer ws.WsWriteLock.Unlock()
	return ws.WsConn.WriteMessage(websocket.TextMessage, data)
}

func (ws *WebSocketSession) SaveSessionId(sessionId string) error {
	dataArray := []interface{}{sessionId, ws.MaxSn}
	data, err := sonic.Marshal(dataArray)
	if err != nil {
		log.WithError(err).Error("SaveSessionId")
		return err
	}
	err = os.WriteFile(ws.SessionFile, data, 0644)
	if err != nil {
		log.WithError(err).Error("SaveSessionId")
		return err
	}
	return nil
}

func (ws *WebSocketSession) Start() {
	ws.StateSession.Start()
	defer ws.HeartBeatCron.Stop()
	defer func() {
		_ = ws.Close()
	}()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	for {
		select {

		case <-interrupt:
			log.Println("interrupt")

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := ws.WsConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			return
		}
	}
}
