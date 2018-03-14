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

package websock

import (
    "testing"
    "errors"
    "strconv"
    "encoding/json"
    "io/ioutil"

    "github.com/AlexRuzin/util"
)

/*
 * Configuration file name
 */
const JSON_FILENAME                 string = "config.json"

/*
 * For example, the config.json file uses the following key/value structure:
 *
 * {
 *   // true -> server/listener mode, false -> client/connect mode
 *   "Server": true,
 *
 *   // Debug is piped to stdout
 *   "Verbosity": true,
 *
 *   // Encryption/compression settings
 *   "Encryption": true,
 *   "Compression": true,
 *
 *   // Connectivity settings for both client and server
 *   "Port": 80,
 *   "Path": "/websock.php",
 *   "Domain": "127.0.0.1",
 *
 *   // If set to true the client will transmit data
 *   "ClientTX": true,
 *
 *   // Data is transmitted between these intervals (milliseconds)
 *   //  i.e every 2 seconds transmit.
 *   "ClientTXTimeMin": 2000,
 *   "ClientTXTimeMax": 2000,
 *
 *   // Transmit data in length between the below intervals (bytes)
 *   //  All data is sent are ASCII capitals between 0x41 - 0x5a
 *   "ClientTXDataMin": 16,
 *   "ClientTXDataMax": 64,
 *
 *   // If this is true, each character will be 'A' 0x41, otherwise
 *   //  they will be ASCII capitals
 *   "ClientTXDataStatic": true,
 *
 *   // If set to true, the server will transmit data to the client.
 *   //  All other settings below follow the above client convention
 *   "ServerTX": true,
 *
 *   "ServerTXTimeMin": 2000,
 *   "ServerTXTimeMax": 2000,
 *
 *   "ServerTXDataMin": 16,
 *   "ServerTXDataMax": 64,
 *
 *   "ServerTXDataStatic": true,
 *
 *   // Do not change this setting
 *   "ModuleName": "websock"
 * }
 */
const moduleName                    string = "websock" /* Do not change this setting */
type configInput struct {
    /* Default test mode */
    Server                          bool

    Verbosity                       bool

    Encryption                      bool
    Compression                     bool

    Port                            uint16
    Path                            string
    Domain                          string

    /* Transmission from client configuration */
    ClientTX                        bool
    ClientTXTimeMin                 uint64
    ClientTXTimeMax                 uint64
    ClientTXDataMin                 uint
    ClientTXDataMax                 uint

    /* Transmission from server configuration */
    ServerTX                        bool
    ServerTXTimeMin                 uint64
    ServerTXTimeMax                 uint64
    ServerTXDataMin                 uint
    ServerTXDataMax                 uint

    /* This value must be static "websock" */
    ModuleName                      string
}

var (
    genericConfig                   *configInput = nil
    mainServer                      *NetChannelService = nil
)
func TestMainChannel(t *testing.T) {
    /* Parse the user input and create a configInput instance */
    config, _ := func () (*configInput, error) {
        /* Read in the configuration file `config.json` */

        rawFile, err := ioutil.ReadFile(JSON_FILENAME)
        if err != nil {
            panic(err)
        }

        /*
         * Build the configInput structure
         */
        var (
            output                  configInput
            parseStatus             error = nil
        )
        parseStatus = json.Unmarshal(rawFile, &output)
        if parseStatus != nil {
            panic(parseStatus)
        }
        if output.ModuleName != moduleName {
            panic(errors.New("invalid configuration file: " + JSON_FILENAME))
        }

        return &output, nil
    } ()
    genericConfig = config

    /* It is absolutely required to use encryption, therefore check for this prior to anything futher */
    if config.Encryption == false {
        panic(errors.New("must use the 'encrypt' flag to 'true'"))
    }

    if config.Verbosity == true {
        func(config *configInput) {
            switch config.Server {
            case false: /* Client mode */
                D("We are running in TYPE_CLIENT mode. Default target server is: " + "http://" +
                    config.Domain + ":" + util.IntToString(int(config.Port)) +
                        config.Path)
                break
            case true: /* Server mode */
                D("We are running in TYPE_SERVER mode. Default listening port is: " +
                    util.IntToString(int(config.Port)))
                D("Default listen path is set to: " + config.Path)
            }

            D("Using encryption [forced]: " + strconv.FormatBool(config.Encryption))
            D("Using compression [optional]: " + strconv.FormatBool(config.Compression))
        }(config)
    }
    D("Configuration file " + JSON_FILENAME + " is nominal, proceeding...")

    switch config.Server {
    case false: /* Client mode */
        var gateURI string = "http://" + config.Domain + ":" + util.IntToString(int(config.Port)) + config.Path
        D("Client target URI is: " + gateURI)

        client, err := BuildChannel(gateURI /* Primary URI (scheme + domain + port + path) */ ,

            /* The below inlines will determine which flags to use based on use input */
            func(useDebug bool) FlagVal {
                if useDebug == true {
                    return FLAG_DEBUG
                }

                return 0
            }(config.Verbosity)|
                func(useEncryption bool) FlagVal {
                    if useEncryption == true {
                        return FLAG_ENCRYPT
                    }

                    return 0
                }(config.Encryption)|
                func(useCompression bool) FlagVal {
                    if useCompression == true {
                        return FLAG_COMPRESS
                    }

                    return 0
                }(config.Compression),
        )
        if err != nil {
            panic(err)
        }
        if err := client.InitializeCircuit(); err != nil {
            panic(err)
        }

        clientTX(*genericConfig)

        break
    case true: /* Server mode */
        D("Server is running on localhost, port: " + util.IntToString(int(config.Port)) +
            ", on HTTP URI path: " + config.Path)

        server, err := CreateServer(config.Path, int16(config.Port),
            /* The below inlines will determine which flags to use based on use input */
            func(useDebug bool) FlagVal {
                if useDebug == true {
                    return FLAG_DEBUG
                }

                return 0
            }(config.Verbosity)|
                func(useEncryption bool) FlagVal {
                    if useEncryption == true {
                        return FLAG_ENCRYPT
                    }

                    return 0
                }(config.Encryption)|
                func(useCompression bool) FlagVal {
                    if useCompression == true {
                        return FLAG_COMPRESS
                    }

                    return 0
                }(config.Compression),
            incomingClientHandler)
        if err != nil {
            panic(err)
        }
        mainServer = server
    }

    /* Wait forever */
    util.WaitForever()
}

func incomingClientHandler(client *NetInstance, server *NetChannelService) error {
    D("Initial connect from client " + client.ClientIdString)

    serverTX(*genericConfig)

    return nil
}

func clientTX(config configInput) {

}

func serverTX(config configInput) {

}

func D(debug string) {
    if genericConfig.Verbosity == true {
        util.DebugOut("[+] " + debug)
    }
}

func T(debug string) {
    util.ThrowN("[!] " + debug)
}
