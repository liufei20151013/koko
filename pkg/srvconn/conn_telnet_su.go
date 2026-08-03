package srvconn

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/jumpserver/koko/pkg/logger"
)

func LoginToTelnetSu(sc *TelnetConnection) error {
	cfg := sc.cfg.suCfg
	logger.Infof("Su: Starting Telnet switch user, method=%s, targetUser=%s",
		cfg.MethodType, cfg.SudoUsername)
	suService, err := NewSuService(cfg, sc)
	if err != nil {
	    logger.Errorf("Su: Failed to create Telnet SuService: %s", err)
		return err
	}
	err = suService.RunSwitchUser()
	if err != nil {
		logger.Errorf("Su: Telnet switch user failed for targetUser=%s, method=%s, err=%s",
			cfg.SudoUsername, cfg.MethodType, err)
	} else {
		logger.Infof("Su: Telnet switch user succeeded for targetUser=%s, method=%s",
			cfg.SudoUsername, cfg.MethodType)
	}
	return err
}

/*

切换用户的执行流程

一、根据系统不同，切换用户执行流程不同
	Linux 系统 sudo 执行流程
	1、执行 su - username; exit (这里的 exit 是为了退出 sudo)
	2、等待密码输入的 prompt (如果是 root 切换普通 可能直接切换成功)

	Cisco 交换机 切换执行流程
	1、执行 enable
	2、等待密码输入的 prompt

	Huawei 交换机 切换执行流程
	1、执行 super 15 (这里的 15 是 user privilege level)
	2、等待密码输入的 prompt

	H3C 交换机 切换执行流程
	1、执行 super level-15 (这里的 15 是 user privilege level)
	2、等待输入 username
	3、等待输入 password

二、等待匹配成功提示字符，如果匹配到失败提示字符，就返回密码错误失败
三、如果成功，返回 切换的提示信息，并通过 \r 换行

关于成功提示符:
Linux 和 Cisco 交换机的成功提示符中，包含
 Huawei:  [root@HUAWEI-xxx]


*/

func NewSuService(cfg *SuConfig, srv io.ReadWriteCloser) (*SuSwitchService, error) {
	logger.Infof("Su: Creating SuSwitchService, method=%s, targetUser=%s, suCommand=%s",
		cfg.MethodType, cfg.SudoUsername, cfg.SuCommand())
	logger.Infof("Su: Success pattern: %s", cfg.SuccessPattern())
	logger.Infof("Su: Password pattern: %s", cfg.PasswordMatchPattern())
	logger.Infof("Su: Username pattern: %s", cfg.UsernameMatchPattern())

	successReg, err := regexp.Compile(cfg.SuccessPattern())
	if err != nil {
		logger.Errorf("Su: Success pattern compile failed: %s, err=%s", cfg.SuccessPattern(), err)
		return nil, fmt.Errorf("success pattern %s compile failed: %s", cfg.SuccessPattern(), err)
	}
	passwordReg, err := regexp.Compile(cfg.PasswordMatchPattern())
	if err != nil {
		logger.Errorf("Su: Password pattern compile failed: %s, err=%s", cfg.PasswordMatchPattern(), err)
		return nil, fmt.Errorf("password pattern %s compile failed: %s", cfg.PasswordMatchPattern(), err)
	}
	usernameReg, err := regexp.Compile(cfg.UsernameMatchPattern())
	if err != nil {
		logger.Errorf("Su: Username pattern compile failed: %s, err=%s", cfg.UsernameMatchPattern(), err)
		return nil, fmt.Errorf("username pattern %s compile failed: %s", cfg.UsernameMatchPattern(), err)
	}
	failedPattern := createFailedPattern()
	logger.Infof("Su: Failure pattern: %s", failedPattern)
	failedReg, err := regexp.Compile(failedPattern)
	if err != nil {
		logger.Errorf("Su: Failure pattern compile failed: %s, err=%s", failedPattern, err)
		return nil, fmt.Errorf("failed pattern %s compile failed: %s", failedPattern, err)
	}
	suService := SuSwitchService{
		cfg:            cfg,
		SrvConn:        srv,
		successRegexp:  successReg,
		usernameRegexp: usernameReg,
		passwordRegexp: passwordReg,
		failureRegexp:  failedReg,
	}
	logger.Infof("Su: SuSwitchService created successfully")
	return &suService, nil
}

type SuSwitchService struct {
	cfg         *SuConfig
	execCommand func()

	SrvConn io.ReadWriteCloser

	successRegexp  *regexp.Regexp
	usernameRegexp *regexp.Regexp
	passwordRegexp *regexp.Regexp
	failureRegexp  *regexp.Regexp

	inputAuthOnce bool
	needAuthOnce  bool
}

func (s *SuSwitchService) RunSwitchUser() error {
	logger.Infof("Su: RunSwitchUser starting, targetUser=%s, method=%s", s.cfg.SudoUsername, s.cfg.MethodType)
	s.runSwitchCommand()
	resultChan := make(chan error, 1)
	go s.loginUsernameOrPassword(resultChan)
	ticker := time.NewTicker(time.Second * 30)
	defer ticker.Stop()
	select {
	case ret := <-resultChan:
		if ret != nil {
			logger.Errorf("Su: RunSwitchUser failed: %s", ret)
		} else {
			logger.Infof("Su: RunSwitchUser succeeded")
		}
		return ret
	case <-ticker.C:
		logger.Errorf("Su: RunSwitchUser timeout after 30s, targetUser=%s, method=%s",
			s.cfg.SudoUsername, s.cfg.MethodType)
	}
	return ErrorTimeout
}

func (s *SuSwitchService) runSwitchCommand() {
	if s.execCommand != nil {
		logger.Infof("Su: Running switch command via execCommand callback")
		s.execCommand()
	} else {
		cmd := s.cfg.SuCommand()
		logger.Infof("Su: Sending switch command directly: %s", cmd)
		_, _ = s.SrvConn.Write([]byte(cmd + "\r"))
		s.needAuthOnce = true
	}
}

func (s *SuSwitchService) loginUsernameOrPassword(resultChan chan<- error) {
	buf := make([]byte, 8192)
	var recStr bytes.Buffer
	loopCount := 0
	for {
		nr, err2 := s.SrvConn.Read(buf)
		if err2 != nil {
			logger.Errorf("Su: Read from server failed, loopCount=%d, err=%s, receivedSoFar=%s",
				loopCount, err2, truncateForLog(recStr.String(), 500))
			resultChan <- err2
			return
		}
		recStr.Write(buf[:nr])
		loopCount++
		logger.Infof("Su: Read cycle #%d, bytes=%d, totalBuffer=%d, data=%s",
			loopCount, nr, recStr.Len(), truncateForLog(string(buf[:nr]), 200))
		status := s.handleResult(recStr.Bytes())
		switch status {
		case StatusSuccess:
			logger.Infof("Su: StatusSuccess - switch user completed, loopCount=%d", loopCount)
			resultChan <- nil
			return
		case StatusMatch:
			recStr.Reset()
			logger.Infof("Su: StatusMatch - pattern matched, buffer reset, loopCount=%d", loopCount)
			continue
		case StatusFailed:
			errMsg := fmt.Sprintf("failed login: %s", truncateForLog(recStr.String(), 500))
			logger.Errorf("Su: StatusFailed - %s, loopCount=%d", errMsg, loopCount)
			resultChan <- fmt.Errorf("failed login: %s", recStr.String())
			return
		case StatusUnMatch:
		default:
		}
		logger.Debugf("Su: StatusUnMatch - no pattern matched yet, loopCount=%d, buffer=%s",
			loopCount, truncateForLog(recStr.String(), 200))
		time.Sleep(time.Millisecond * 100)
	}
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

func (s *SuSwitchService) handleResult(p []byte) matchStatus {
	newBytes := bytes.ReplaceAll(p, []byte("\r"), []byte("\n"))
	newBytes = bytes.ReplaceAll(newBytes, []byte("\n\n"), []byte("\n"))
	lineBytes := bytes.Split(newBytes, []byte("\n"))

	rawPreview := truncateForLog(string(p), 300)

	if s.usernameRegexp != nil && s.usernameRegexp.Match(p) {
		for _, line := range lineBytes {
			if s.usernameRegexp.Match(line) {
				logger.Infof("Su: Username prompt matched, sending username=%s, matchedLine=%s",
					s.cfg.SudoUsername, string(line))
				_, _ = s.SrvConn.Write([]byte(s.cfg.SudoUsername + "\r"))
				return StatusMatch
			}
		}
	}
	if s.passwordRegexp != nil {
		for _, line := range lineBytes {
			if s.passwordRegexp.Match(line) {
				logger.Infof("Su: Password prompt matched, sending password (len=%d), matchedLine=%s",
					len(s.cfg.SudoPassword), string(line))
				_, _ = s.SrvConn.Write([]byte(s.cfg.SudoPassword + "\r"))
				s.inputAuthOnce = true
				return StatusMatch
			}
		}
	}
	if s.needAuthOnce && s.inputAuthOnce {
		if s.failureRegexp != nil {
			for _, line := range lineBytes {
				if s.failureRegexp.Match(line) {
					logger.Errorf("Su: Failure pattern matched, line=%s, fullOutput=%s",
						string(line), rawPreview)
					return StatusFailed
				}
			}
		}
	}
	if s.successRegexp != nil {
		if s.needAuthOnce && !s.inputAuthOnce {
			logger.Infof("Su: Success check skipped - needAuthOnce=true but no auth sent yet, needAuthOnce=%v, inputAuthOnce=%v",
				s.needAuthOnce, s.inputAuthOnce)
			return StatusUnMatch
		}
		for _, line := range lineBytes {
			if s.successRegexp.Match(line) {
				logger.Infof("Su: Success pattern matched, line=%s, fullOutput=%s",
					string(line), rawPreview)
				return StatusSuccess
			}
		}
	}
	// 输出未匹配详情（每条line）
	logger.Debugf("Su: No pattern matched, needAuthOnce=%v, inputAuthOnce=%v, lines=%d, preview=%s",
		s.needAuthOnce, s.inputAuthOnce, len(lineBytes), rawPreview)
	for i, line := range lineBytes {
		if len(line) > 0 {
			logger.Debugf("Su:   line[%d]=%s", i, string(line))
		}
	}
	return StatusUnMatch
}

type matchStatus int

const (
	StatusUnMatch matchStatus = 1
	StatusMatch   matchStatus = 2
	StatusSuccess matchStatus = 3
	StatusFailed  matchStatus = 4
)
