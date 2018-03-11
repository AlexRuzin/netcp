/*
 * Copyright (c) 2017 AlexRuzin (stan.ruzin@gmail.com)
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

/* Commit test */

package websock

import (
    "fmt"
    "sync"
    "bytes"
    "strings"
    "io"
    "time"
    "net/http"
    "crypto/elliptic"
    "crypto/rand"
    "crypto/md5"
    "hash/crc64"
    "encoding/hex"
    "encoding/gob"

    "github.com/AlexRuzin/cryptog"
    "github.com/AlexRuzin/util"
    "github.com/wsddn/go-ecdh"
)

/************************************************************
 * websock Server objects and methods  f                    *
 ************************************************************/
var clientIO chan *NetInstance = nil
var channelService *NetChannelService = nil

type NetChannelService struct {
    /* Handler for new clients */
    IncomingHandler         func(client *NetInstance, server *NetChannelService) error

    /* Flags may be modified at any time */
    Flags                   FlagVal

    /* Non-exported members */
    port                    int16
    pathGate                string
    clientMap               map[string]*NetInstance
    clientIO                chan *NetInstance
    clientSync              sync.Mutex
}

type NetInstance struct {
    /* Unique identifier that represents the client connection */
    ClientIdString          string

    /* Non-exported members */
    service                 *NetChannelService
    secret                  []byte
    clientId                []byte
    clientTX                *bytes.Buffer /* Data waiting to be transmitted */
    clientRX                *bytes.Buffer /* Data that is waiting to be read */
    iOSync                  sync.Mutex

    connected               bool

    /* URI Path */
    RequestURI              string
}

func (f *NetInstance) Len() int {
    f.iOSync.Lock()
    defer f.iOSync.Unlock()

    return f.clientRX.Len()
}

func (f *NetInstance) Wait(timeoutMilliseconds time.Duration) (responseLen int, err error) {
    if f.connected == false {
        return 0, util.RetErrStr("client not connected")
    }

    responseLen = 0
    err = WAIT_TIMEOUT_REACHED

    for i := timeoutMilliseconds / 100; i != 0; i -= 1 {
        if f.connected == false {
            responseLen = -1
            err = WAIT_CLOSED
            break
        }

        if f.Len() > 0 {
            responseLen = f.Len()
            err = WAIT_DATA_RECEIVED
            break
        }

        util.Sleep(100 * time.Millisecond)
    }

    return
}

func (f *NetInstance) readInternal(p []byte) (int, error) {
    if f.connected == false {
        return 0, util.RetErrStr("client not connected")
    }

    if f.clientRX.Len() == 0 {
        return 0, io.EOF
    }

    f.iOSync.Lock()
    defer f.iOSync.Unlock()

    data := make([]byte, f.clientRX.Len())
    f.clientRX.Read(data)
    f.clientRX.Reset()
    copy(p, data)

    return len(data), io.EOF
}

func (f *NetInstance) writeInternal(p []byte) (int, error) {
    if f.connected == false {
        return 0, util.RetErrStr("client not connected")
    }

    f.iOSync.Lock()
    defer f.iOSync.Unlock()

    f.clientTX.Write(p)

    return len(p), io.EOF
}

func (f *NetInstance) Read(p []byte) (read int, err error) {
    read, err = f.readInternal(p)
    if err != nil {
        return 0, err
    }

    return
}

func (f *NetInstance) Write(p []byte) (wrote int, err error) {
    wrote, err = f.writeInternal(p)
    if err != nil {
        return 0, err
    }

    return
}

/* Create circuit -OR- process gate requests */
func handleClientRequest(writer http.ResponseWriter, reader *http.Request) {
    if clientIO == nil {
        panic(util.RetErrStr("Cannot handle request without initializing processor"))
    }

    defer reader.Body.Close()

    /* Get remote client public key base64 marshalled string */
    if err := reader.ParseForm(); err != nil {
        util.DebugOut(err.Error())
        return
    }
    const cs = POST_BODY_KEY_CHARSET
    var marshalled_client_pub_key *string = nil
    for key := range reader.Form {
        for i := len(POST_BODY_KEY_CHARSET); i != 0; i -= 1 {
            var tmp_key = string(cs[i - 1])

            decoded_key, err := util.B64D(key)
            if err != nil {
                return
            }

            if strings.Compare(tmp_key, string(decoded_key)) == 0 {
                marshalled_client_pub_key = &reader.Form[key][0]
                break
            }
        }
        if marshalled_client_pub_key != nil {
            break
        }
    }

    if marshalled_client_pub_key == nil {
        /*
         * Parameter for key negotiation does not exist. This implies that either someone is not using
         *  the server in the designed fashion, or that there is another command request coming from
         *  and existing client. Here we verify if the client exists.
         *
         * If it's a command, then there should be only one parameter, which is:
         *  b64(ClientIdString) = <command>
         */
         key := reader.Form
         if key == nil {
             return
         }

         for k := range key {
             var err error = nil
             var decoded_key []byte
             if decoded_key, err = util.B64D(k); err != nil {
                 continue
             }
             client := channelService.clientMap[string(decoded_key)]
             if client != nil {
                 /*
                  * An active connection exists.
                  *
                  * Base64 decode the signal and return the RC4 encrypted buffer to
                  *  be processed
                  *
                  * Write data to NetInstance.ClientData
                  */
                 value := key[k]
                 var client_id string
                 var data []byte = nil
                 if client_id, data, err = decryptData(value[0], client.secret);
                 err != nil || strings.Compare(client_id, client.ClientIdString) != 0 {
                     channelService.CloseClient(client)
                     return
                 }

                 if err := client.parseClientData(data, writer); err != nil {
                     channelService.CloseClient(client)
                     return
                 }

                 return /* The appropriate ClientData has been stored, so no more need for this method */
             }
         }
    }

    /* Parse client-side public ECDH key*/
    marshalled, err := getClientPublicKey(*marshalled_client_pub_key)
    if err != nil || marshalled == nil {
        sendBadErrorCode(writer, err)
        util.DebugOut(err.Error())
        return
    }

    ecurve := ecdh.NewEllipticECDH(elliptic.P384())
    clientPublicKey, ok := ecurve.Unmarshal(marshalled)
    if !ok {
        sendBadErrorCode(writer, util.RetErrStr("unmarshalling failed"))
        return
    }

    /*
     * Since the client public key is nominal return generate
     *  our own keypair
     */
    serverPrivateKey, serverPublicKey, err := ecurve.GenerateKey(rand.Reader)
    if err != nil {
        sendBadErrorCode(writer, err)
        return
    }

    /* Transmit the server public key */
    var serverPubKeyMarshalled = ecurve.Marshal(serverPublicKey)
    if serverPubKeyMarshalled == nil {
        sendBadErrorCode(writer, util.RetErrStr("Failed to marshal server-side pub key"))
        return
    }
    client_id := md5.Sum(marshalled)
    if err := sendPubKey(writer, serverPubKeyMarshalled, client_id[:]); err != nil {
        sendBadErrorCode(writer, err)
        return
    }

    /* Generate the secret */
    secret, err := ecurve.GenerateSharedSecret(serverPrivateKey, clientPublicKey)
    if len(secret) == 0 {
        sendBadErrorCode(writer, util.RetErrStr("Failed to generate a shared secret key"))
        return
    }

    if (channelService.Flags & FLAG_DEBUG) > 1 {
        util.DebugOut("Server-side secret:")
        util.DebugOutHex(secret)
    }

    var instance = &NetInstance{
        service:            channelService,
        secret:             secret,
        clientId:           client_id[:],
        ClientIdString:     hex.EncodeToString(client_id[:]),
        clientRX:           &bytes.Buffer{},
        clientTX:           &bytes.Buffer{},
        connected:          false,
        RequestURI:         reader.RequestURI,
    }

    clientIO <- instance
}

func (f *NetInstance) parseClientData(rawData []byte, writer http.ResponseWriter) error {
    /*
     * Check for internal commands first
     */
    if util.IsAsciiPrintable(string(rawData)) {
        var command = string(rawData)

        switch command {
        case CHECK_STREAM_DATA:
            if f.connected == false {
                return util.RetErrStr("client not connected")
            }

            var timeout = CONTROLLER_RESPONSE_TIMEOUT * 100
            for ; timeout != 0; timeout -= 1 {
                if f.clientTX.Len() != 0 {
                    f.iOSync.Lock()
                    break
                }
                util.Sleep(10 * time.Millisecond)
            }

            if timeout == 0 {
                /* Time out -- no data to be sent */
                if f.clientTX.Len() == 0 {
                    writer.WriteHeader(http.StatusOK)
                    return nil
                }
            }

            defer f.clientTX.Reset()
            defer f.iOSync.Unlock()

            var outputStream []byte = f.clientTX.Bytes()

            if (channelService.Flags & FLAG_COMPRESS) > 0 {
                var streamStatus error = nil
                outputStream, streamStatus = util.CompressStream(outputStream)
                if streamStatus != nil {
                    panic(streamStatus)
                }
            }

            encrypted, _ := encryptData(outputStream, f.secret, FLAG_DIRECTION_TO_CLIENT, 0, f.ClientIdString)
            return sendResponse(writer, encrypted)

        case TEST_CONNECTION_DATA:
            encrypted, _ := encryptData(rawData, f.secret, FLAG_DIRECTION_TO_CLIENT, 0, f.ClientIdString)
            return sendResponse(writer, encrypted)

        case TERMINATE_CONNECTION_DATA:
            /* FIXME */
            panic("terminating connection")
        }
    }

    /* Append data to read */
    if f.connected == false {
        return util.RetErrStr("client not connected")
    }

    f.iOSync.Lock()
    defer f.iOSync.Unlock()

    var requestData []byte = rawData

    f.clientRX.Write(requestData)

    /* If there is any data to return, then send it over */
    if f.clientTX.Len() > 0 {
        defer f.clientTX.Reset()

        var outputStream []byte = f.clientTX.Bytes()

        if (channelService.Flags & FLAG_COMPRESS) > 0 {
            var streamStatus error = nil
            outputStream, streamStatus = util.CompressStream(outputStream)
            if streamStatus != nil {
                panic(streamStatus)
            }
        }

        encrypted, _ := encryptData(outputStream, f.secret, FLAG_DIRECTION_TO_CLIENT, 0, f.ClientIdString)
        return sendResponse(writer, encrypted)
    }
    writer.WriteHeader(http.StatusOK)

    return nil
}

func decryptData(b64_encoded string, secret []byte) (client_id string, raw_data []byte, status error) {
    status = util.RetErrStr("decryptData: Unknown error")
    client_id = ""
    raw_data = nil

    b64_decoded, err := util.B64D(b64_encoded)
    if err != nil {
        status = err
        return
    }

    decrypted, err := cryptog.RC4_Decrypt(b64_decoded, cryptog.RC4_PrepareKey(secret))
    if err != nil {
        status = err
        return
    }

    tx_unit, err := func(raw []byte) (*TransferUnit, error) {
        output := new(TransferUnit)

        p := &bytes.Buffer{}
        p.Write(raw)
        d := gob.NewDecoder(p)
        if err := d.Decode(output); err != nil {
            return nil, err
        }

        return output, nil
    } (decrypted)
    if err != nil || tx_unit == nil {
        status = err
        return
    }

    new_sum := func (p []byte) string {
        data_sum := md5.Sum(p)
        return hex.EncodeToString(data_sum[:])
    } (tx_unit.Data)
    if strings.Compare(new_sum, tx_unit.DecryptedSum) != 0 {
        status = util.RetErrStr("decryptData: Data corruption")
        return
    }

    raw_data = tx_unit.Data
    client_id = tx_unit.ClientID
    status = nil
    return
}

func getClientPublicKey(buffer string) (marshalled_pub_key []byte, err error) {
    /*
     * Read in an HTTP request in the following format:
     *  b64([8 bytes XOR key][XOR-SHIFT encrypted marshalled public ECDH key][md5sum of first 2])
     */
    b64_decoded, err := util.B64D(buffer)
    if err != nil {
        return nil, err
    }
    var xor_key = make([]byte, crc64.Size)
    copy(xor_key, b64_decoded[:crc64.Size])
    var marshal_xor = make([]byte, len(b64_decoded) - crc64.Size - md5.Size)
    var sum = make([]byte, md5.Size)
    copy(sum, b64_decoded[len(xor_key) + len(marshal_xor):])

    sum_buffer := make([]byte, len(b64_decoded) - md5.Size)
    copy(sum_buffer, b64_decoded[:len(b64_decoded) - md5.Size])
    new_sum := md5.Sum(sum_buffer)
    if !bytes.Equal(new_sum[:], sum) {
        return nil, util.RetErrStr("Data integrity mismatch")
    }

    copy(marshal_xor, b64_decoded[crc64.Size:len(b64_decoded) - md5.Size])
    marshalled := func (key []byte, pool []byte) []byte {
        var output = make([]byte, len(pool))
        copy(output, pool)

        counter := 0
        for k := range pool {
            if counter == 8 {
                counter = 0
            }
            output[k] = output[k] ^ key[counter]
            counter += 1
        }

        return output
    } (xor_key, marshal_xor)

    return marshalled, nil
}

/* HTTP 500 - Internal Server Error */
func sendBadErrorCode(writer http.ResponseWriter, err error) {
    writer.WriteHeader(http.StatusInternalServerError)
    writer.Write([]byte("500 - " + err.Error()))
    return
}

/* Send back server pub key */
func sendPubKey(writer http.ResponseWriter, marshalled []byte, client_id []byte) error {
    var pool = bytes.Buffer{}
    var xor_key = make([]byte, crc64.Size)
    rand.Read(xor_key)
    pool.Write(xor_key)
    marshalled_xord := make([]byte, len(marshalled))
    copy(marshalled_xord, marshalled)
    counter := 0
    for k := range marshalled_xord {
        if counter == len(xor_key) {
            counter = 0
        }

        marshalled_xord[k] ^= xor_key[counter]
        counter += 1
    }
    pool.Write(marshalled_xord)
    pool.Write(client_id)

    if err := sendResponse(writer, pool.Bytes()); err != nil {
        return err
    }

    return nil
}

func sendResponse(writer http.ResponseWriter, data []byte) error {
    if len(data) == 0 {
        return util.RetErrStr("sendResponse: Invalid parameter")
    }

    var b64_encoded = util.B64E(data)

    writer.Header().Set("Content-Type", HTTP_CONTENT_TYPE)
    writer.Header().Set("Connection", "close")
    writer.WriteHeader(http.StatusOK)

    fmt.Fprintln(writer, b64_encoded)

    return nil
}

func (f *NetChannelService) CloseClient(client *NetInstance) {
    f.clientSync.Lock()
    delete(f.clientMap, client.ClientIdString)
    f.clientSync.Unlock()
}

func (f *NetChannelService) CloseService() {
    if clientIO != nil {
        close(clientIO)
    }
}

func (f *NetInstance) Close() {
    channelService.CloseClient(f)
}

func CreateServer(path_gate string, port int16, flags FlagVal, handler func(client *NetInstance,
    server *NetChannelService) error) (*NetChannelService, error) {

    /* The FLAG_ENCRYPT switch must always be set to true */
    if (flags & FLAG_ENCRYPT) == 0 {
        return nil, util.RetErrStr("FLAG_ENCRYPT must be set")
    }

    var server = &NetChannelService{
        IncomingHandler: handler,
        port: port,
        Flags: flags,
        pathGate: path_gate,

        /* Map consists of key: ClientId (string) and value: *NetInstance object */
        clientMap: make(map[string]*NetInstance),
        clientIO: make(chan *NetInstance),
    }
    clientIO = server.clientIO
    channelService = server

    go func (svc *NetChannelService) {
        var wg sync.WaitGroup
        wg.Add(1)

        for {
            client, ok := <- svc.clientIO
            if !ok {
                break /* Close the processor */
            }

            svc.clientSync.Lock()

            svc.clientMap[client.ClientIdString] = client
            if err := svc.IncomingHandler(client, svc); err != nil {
                svc.CloseClient(client)
            }

            svc.clientSync.Unlock()
            client.connected = true
        }
    } (server)

    go func(svc *NetChannelService) {
        /* FIXME -- find a way of closing this thread once CloseService() is invoked */
        http.HandleFunc(server.pathGate, handleClientRequest)

        svc.sendDebug("Handling request for path :" + svc.pathGate)
        if err := http.ListenAndServe(":" + util.IntToString(int(server.port)),nil); err != nil {
            util.ThrowN("panic: Failure in loading httpd.")
        }
    } (server)

    return server, nil
}

func (f *NetChannelService) sendDebug(s string) {
    if (f.Flags & FLAG_DEBUG) > 0 {
        util.DebugOut(s)
    }
}