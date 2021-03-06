// https://tools.ietf.org/html/rfc1035

package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
)

// DNSHeaderLen is the length of dns msg header
const DNSHeaderLen = 12

// DNSUDPMaxLen is the max size of udp dns request.
// https://tools.ietf.org/html/rfc1035#section-4.2.1
// Messages carried by UDP are restricted to 512 bytes (not counting the IP
// or UDP headers).  Longer messages are truncated and the TC bit is set in
// the header.
// TODO: If the request length > 512 then the client will send TCP packets instead,
// so we should also serve tcp requests.
const DNSUDPMaxLen = 512

// DNSQTypeA ipv4
const DNSQTypeA = 1

// DNSQTypeAAAA ipv6
const DNSQTypeAAAA = 28

// DNSMsg format
// https://tools.ietf.org/html/rfc1035#section-4.1
// All communications inside of the domain protocol are carried in a single
// format called a message.  The top level format of message is divided
// into 5 sections (some of which are empty in certain cases) shown below:
//
//     +---------------------+
//     |        Header       |
//     +---------------------+
//     |       Question      | the question for the name server
//     +---------------------+
//     |        Answer       | RRs answering the question
//     +---------------------+
//     |      Authority      | RRs pointing toward an authority
//     +---------------------+
//     |      Additional     | RRs holding additional information
// type DNSMsg struct {
// 	DNSHeader
// 	Questions []DNSQuestion
// 	Answers   []DNSRR
// }

// DNSHeader format
// https://tools.ietf.org/html/rfc1035#section-4.1.1
// The header contains the following fields:
//
//                                     1  1  1  1  1  1
//       0  1  2  3  4  5  6  7  8  9  0  1  2  3  4  5
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                      ID                       |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |QR|   Opcode  |AA|TC|RD|RA|   Z    |   RCODE   |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                    QDCOUNT                    |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                    ANCOUNT                    |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                    NSCOUNT                    |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                    ARCOUNT                    |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//
// type DNSHeader struct {
// 	ID uint16
// }

// DNSQuestion format
// https://tools.ietf.org/html/rfc1035#section-4.1.2
// The question section is used to carry the "question" in most queries,
// i.e., the parameters that define what is being asked.  The section
// contains QDCOUNT (usually 1) entries, each of the following format:
//
//                                     1  1  1  1  1  1
//       0  1  2  3  4  5  6  7  8  9  0  1  2  3  4  5
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                                               |
//     /                     QNAME                     /
//     /                                               /
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                     QTYPE                     |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                     QCLASS                    |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
type DNSQuestion struct {
	QNAME  string
	QTYPE  uint16
	QCLASS uint16

	Offset int
}

// DNSRR format
// https://tools.ietf.org/html/rfc1035#section-3.2.1
// All RRs have the same top level format shown below:
//
//                                     1  1  1  1  1  1
//       0  1  2  3  4  5  6  7  8  9  0  1  2  3  4  5
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                                               |
//     /                                               /
//     /                      NAME                     /
//     |                                               |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                      TYPE                     |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                     CLASS                     |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                      TTL                      |
//     |                                               |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
//     |                   RDLENGTH                    |
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--|
//     /                     RDATA                     /
//     /                                               /
//     +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
type DNSRR struct {
	// NAME string
	TYPE     uint16
	CLASS    uint16
	TTL      uint32
	RDLENGTH uint16
	RDATA    []byte

	IP string
}

// DNSAnswerHandler function handles the dns TypeA or TypeAAAA answer
type DNSAnswerHandler func(Domain, ip string) error

// DNS .
type DNS struct {
	*Forwarder        // as proxy client
	sDialer    Dialer // dialer for server

	Tunnel bool

	DNSServer string

	DNSServerMap   map[string]string
	AnswerHandlers []DNSAnswerHandler
}

// NewDNS returns a dns forwarder. client[dns.udp] -> glider[tcp] -> forwarder[dns.tcp] -> remote dns addr
func NewDNS(addr, raddr string, sDialer Dialer, tunnel bool) (*DNS, error) {
	s := &DNS{
		Forwarder: NewForwarder(addr, nil),
		sDialer:   sDialer,

		Tunnel: tunnel,

		DNSServer:    raddr,
		DNSServerMap: make(map[string]string),
	}

	return s, nil
}

// ListenAndServe .
func (s *DNS) ListenAndServe() {
	go s.ListenAndServeTCP()
	s.ListenAndServeUDP()
}

// ListenAndServeUDP .
func (s *DNS) ListenAndServeUDP() {
	c, err := net.ListenPacket("udp", s.addr)
	if err != nil {
		logf("proxy-dns failed to listen on %s, error: %v", s.addr, err)
		return
	}
	defer c.Close()

	logf("proxy-dns listening UDP on %s", s.addr)

	for {
		b := make([]byte, DNSUDPMaxLen)
		n, clientAddr, err := c.ReadFrom(b)
		if err != nil {
			logf("proxy-dns local read error: %v", err)
			continue
		}

		reqLen := uint16(n)
		// TODO: check here
		if reqLen <= DNSHeaderLen+2 {
			logf("proxy-dns not enough data")
			continue
		}

		reqMsg := b[:n]
		go func() {
			_, respMsg, err := s.Exchange(reqLen, reqMsg, clientAddr.String())
			if err != nil {
				logf("proxy-dns error in exchange: %s", err)
				return
			}

			_, err = c.WriteTo(respMsg, clientAddr)
			if err != nil {
				logf("proxy-dns error in local write: %s", err)
				return
			}

		}()
	}
}

// ListenAndServeTCP .
func (s *DNS) ListenAndServeTCP() {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		logf("proxy-dns-tcp error: %v", err)
		return
	}

	logf("proxy-dns-tcp listening TCP on %s", s.addr)

	for {
		c, err := l.Accept()
		if err != nil {
			logf("proxy-dns-tcp error: failed to accept: %v", err)
			continue
		}
		go s.ServeTCP(c)
	}
}

// ServeTCP .
func (s *DNS) ServeTCP(c net.Conn) {
	defer c.Close()

	if c, ok := c.(*net.TCPConn); ok {
		c.SetKeepAlive(true)
	}

	var reqLen uint16
	if err := binary.Read(c, binary.BigEndian, &reqLen); err != nil {
		logf("proxy-dns-tcp failed to get request length: %v", err)
		return
	}

	// TODO: check here
	if reqLen <= DNSHeaderLen+2 {
		logf("proxy-dns-tcp not enough data")
		return
	}

	reqMsg := make([]byte, reqLen)
	_, err := io.ReadFull(c, reqMsg)
	if err != nil {
		logf("proxy-dns-tcp error in read reqMsg %s", err)
		return
	}

	respLen, respMsg, err := s.Exchange(reqLen, reqMsg, c.RemoteAddr().String())
	if err != nil {
		logf("proxy-dns-tcp error in exchange: %s", err)
		return
	}

	if err := binary.Write(c, binary.BigEndian, respLen); err != nil {
		logf("proxy-dns-tcp error in local write respLen: %s", err)
		return
	}
	if err := binary.Write(c, binary.BigEndian, respMsg); err != nil {
		logf("proxy-dns-tcp error in local write respMsg: %s", err)
		return
	}
}

// Exchange handles request msg and returns response msg
// TODO: multiple questions support, parse header to get the number of questions
func (s *DNS) Exchange(reqLen uint16, reqMsg []byte, addr string) (respLen uint16, respMsg []byte, err error) {
	// fmt.Printf("\ndns req len %d:\n%s\n", reqLen, hex.Dump(reqMsg[:]))
	query, err := parseQuestion(reqMsg)
	if err != nil {
		logf("proxy-dns error in parseQuestion reqMsg: %s", err)
		return
	}

	dnsServer := s.DNSServer
	if !s.Tunnel {
		dnsServer = s.GetServer(query.QNAME)
	}

	rc, err := s.sDialer.NextDialer(query.QNAME+":53").Dial("tcp", dnsServer)
	if err != nil {
		logf("proxy-dns failed to connect to server %v: %v", dnsServer, err)
		return
	}
	defer rc.Close()

	if err = binary.Write(rc, binary.BigEndian, reqLen); err != nil {
		logf("proxy-dns failed to write req length: %v", err)
		return
	}
	if err = binary.Write(rc, binary.BigEndian, reqMsg); err != nil {
		logf("proxy-dns failed to write req message: %v", err)
		return
	}

	if err = binary.Read(rc, binary.BigEndian, &respLen); err != nil {
		logf("proxy-dns failed to read response length: %v", err)
		return
	}

	respMsg = make([]byte, respLen)
	_, err = io.ReadFull(rc, respMsg)
	if err != nil {
		logf("proxy-dns error in read respMsg %s\n", err)
		return
	}

	// fmt.Printf("\ndns resp len %d:\n%s\n", respLen, hex.Dump(respMsg[:]))

	var ip string
	respReq, err := parseQuestion(respMsg)
	if err != nil {
		logf("proxy-dns error in parseQuestion respMsg: %s", err)
		return
	}

	if (respReq.QTYPE == DNSQTypeA || respReq.QTYPE == DNSQTypeAAAA) &&
		len(respMsg) > respReq.Offset {

		var answers []*DNSRR
		answers, err = parseAnswers(respMsg[respReq.Offset:])
		if err != nil {
			logf("proxy-dns error in parseAnswers: %s", err)
			return
		}

		for _, answer := range answers {
			for _, h := range s.AnswerHandlers {
				h(respReq.QNAME, answer.IP)
			}

			if answer.IP != "" {
				ip += answer.IP + ","
			}
		}

	}

	logf("proxy-dns %s <-> %s, type: %d, %s: %s", addr, dnsServer, query.QTYPE, query.QNAME, ip)
	return
}

// SetServer .
func (s *DNS) SetServer(domain, server string) {
	s.DNSServerMap[domain] = server
}

// GetServer .
func (s *DNS) GetServer(domain string) string {
	domainParts := strings.Split(domain, ".")
	length := len(domainParts)
	for i := length - 2; i >= 0; i-- {
		domain := strings.Join(domainParts[i:length], ".")

		if server, ok := s.DNSServerMap[domain]; ok {
			return server
		}
	}

	return s.DNSServer
}

// AddAnswerHandler .
func (s *DNS) AddAnswerHandler(h DNSAnswerHandler) {
	s.AnswerHandlers = append(s.AnswerHandlers, h)
}

func parseQuestion(p []byte) (*DNSQuestion, error) {
	q := &DNSQuestion{}
	lenP := len(p)

	var i int
	var domain []byte
	for i = DNSHeaderLen; i < lenP; {
		l := int(p[i])

		if l == 0 {
			i++
			break
		}

		if lenP <= i+l+1 {
			return nil, errors.New("not enough data for QNAME")
		}

		domain = append(domain, p[i+1:i+l+1]...)
		domain = append(domain, '.')

		i = i + l + 1
	}

	if len(domain) == 0 {
		return nil, errors.New("no QNAME")
	}

	q.QNAME = string(domain[:len(domain)-1])

	if lenP < i+4 {
		return nil, errors.New("not enough data")
	}

	q.QTYPE = binary.BigEndian.Uint16(p[i:])
	q.QCLASS = binary.BigEndian.Uint16(p[i+2:])
	q.Offset = i + 4

	return q, nil
}

func parseAnswers(p []byte) ([]*DNSRR, error) {
	var answers []*DNSRR
	lenP := len(p)

	for i := 0; i < lenP; {

		// https://tools.ietf.org/html/rfc1035#section-4.1.4
		// "Message compression",
		// +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+
		// | 1  1|                OFFSET                   |
		// +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+

		if p[i]>>6 == 3 {
			i += 2
		} else {
			// TODO: none compressed query name and Additional records will be ignored
			break
		}

		if lenP <= i+10 {
			return nil, errors.New("not enough data")
		}

		answer := &DNSRR{}

		answer.TYPE = binary.BigEndian.Uint16(p[i:])
		answer.CLASS = binary.BigEndian.Uint16(p[i+2:])
		answer.TTL = binary.BigEndian.Uint32(p[i+4:])
		answer.RDLENGTH = binary.BigEndian.Uint16(p[i+8:])
		answer.RDATA = p[i+10 : i+10+int(answer.RDLENGTH)]

		if answer.TYPE == DNSQTypeA {
			answer.IP = net.IP(answer.RDATA[:net.IPv4len]).String()
		} else if answer.TYPE == DNSQTypeAAAA {
			answer.IP = net.IP(answer.RDATA[:net.IPv6len]).String()
		}

		answers = append(answers, answer)

		i = i + 10 + int(answer.RDLENGTH)
	}

	return answers, nil
}
