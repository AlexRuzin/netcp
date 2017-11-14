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

package netcp

import (
    "errors"
    "io"
    "bytes"
    "net/url"
    "net/http"
    "github.com/wsddn/go-ecdh"
    "crypto/elliptic"
    "hash/crc64"
    "crypto/md5"
    "crypto/rand"
    "crypto"
    "encoding/base64"
    "github.com/AlexRuzin/util"
)

/************************************************************
 * netcp Client objects and methods                         *
 ************************************************************/

type NetChannelClient struct {
    InputURI        string
    Port            int16
    Flags           int
    Connected       bool
    Path            string
    Host            string
    URL             *url.URL
    PrivateKey      *crypto.PrivateKey
    PublicKey       *crypto.PublicKey
    ServerPublicKey *crypto.PrivateKey
}

func BuildNetCPChannel(gate_uri string, port int16, flags int) (*NetChannelClient, error) {
    if flags == -1 {
        return nil, errors.New("error: BuildNetCPChannel: invalid flag: -1")
    }

    main_url, err := url.Parse(gate_uri)
    if err != nil {
        return nil, err
    }
    if main_url.Scheme != "http" {
        return nil, errors.New("error: HTTP scheme must not use TLS")
    }

    var io_channel = &NetChannelClient{
        URL: main_url,
        InputURI: gate_uri,
        Port: port,
        Flags: 0,
        Connected: false,
        Path: main_url.Path,
        Host: main_url.Host,
        PrivateKey: nil,
        PublicKey: nil,
        ServerPublicKey: nil,
    }

    return io_channel, nil
}

/*
 * Check if the TCP port is reachcable, then attempt to determine
 *  if our service is running on it
 */
func encodeKeyValue (high int) (string) {
    return base64.StdEncoding.EncodeToString([]byte(util.RandomString(util.RandInt(1, high))))
}

func (f *NetChannelClient) InitializeCircuit() error {
    post_pool, err := f.genTxPool()
    if err != nil || len(post_pool) < 1 {
        return err
    }

    /* This is the parameter that will hold our secret data */


    /* generate fake key/value pools */
    var parm_map = make(map[string]string)
    num_of_parameters := util.RandInt(3, POST_BODY_JUNK_MAX_PARAMETERS)

    magic_number := num_of_parameters / 2
    for i := num_of_parameters; i != 0; i -= 1 {
        var pool, key string
        if POST_BODY_VALUE_LEN != -1 {
            pool = encodeKeyValue(POST_BODY_VALUE_LEN)
        } else {
            pool = encodeKeyValue(len(string(post_pool)) * 2)
        }
        key = encodeKeyValue(POST_BODY_KEY_LEN)

        parm_map[key] = pool

        if i == magic_number {
            parm_map[POST_PARAM_NAME] = string(post_pool)
        }
    }

    /* Perform HTTP TX */
    resp, tx_err := func(method string,
            URI string,
            m map[string]string) (response *http.Response, err error) {
        req, err := http.NewRequest(method /* POST */, URI, nil)
        if err != nil {
            return nil, err
        }

        form := req.URL.Query()
        for k, v := range m {
            form.Add(k, v)
        }

        /* "application/x-www-form-urlencoded" */
        req.Header.Set("Content-Type", HTTP_CONTENT_TYPE)

        req.Header.Set("Connection", "close")

        req.Header.Set("User-Agent", HTTP_USER_AGENT)
        req.Header.Set("Host", URI) // FIXME -- check that the URI is correct for Host!!!

        req.URL.RawQuery = form.Encode()

        client := &http.Client{}
        resp, err := client.Do(req)
        if err != nil {
            return nil, err
        }
        defer resp.Body.Close()

        return resp, nil
    } ("POST", f.InputURI, parm_map)
    if tx_err != nil && tx_err != io.EOF {
        return tx_err
    }

    if resp.Status != "200 OK" {
        return errors.New("HTTP 200 OK not returned")
    }

    return nil
}

func (f *NetChannelClient) genTxPool() ([]byte, error) {
    /*
     * Generate the ECDH keys based on the EllipticP384 Curve
     */
    curve := ecdh.NewEllipticECDH(elliptic.P384())
    clientPrivateKey, clientPublicKey, err := curve.GenerateKey(rand.Reader)
    if err != nil {
        return nil, err
    }
    f.PublicKey = &clientPublicKey
    f.PrivateKey = &clientPrivateKey

    /***********************************************************************************************
     * Tranmis the public key ECDH key to server. The transmission buffer contains:                *
     *  b64([8 bytes XOR key][XOR-SHIFT encrypted marshalled public ECDH key][md5sum of first 2])  *
     ***********************************************************************************************/
    var pubKeyMarshalled = curve.Marshal(clientPublicKey)
    var pool = bytes.Buffer{}
    tmp := make([]byte, crc64.Size)
    rand.Read(tmp)
    pool.Write(tmp)
    marshal_encrypted := make([]byte, len(pubKeyMarshalled))
    copy(marshal_encrypted, pubKeyMarshalled)
    counter := 0
    for k := range marshal_encrypted {
        if counter == len(tmp) {
            counter = 0
        }
        marshal_encrypted[k] ^= tmp[counter]
        counter += 1
    }
    pool.Write(marshal_encrypted)
    pool_sum := md5.Sum(pool.Bytes())
    pool.Write(pool_sum[:])

    b64_buf := base64.StdEncoding.EncodeToString(pool.Bytes())
    return []byte(b64_buf), nil
}