package resource_test

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	"code.cloudfoundry.org/garden"
	gfakes "code.cloudfoundry.org/garden/gardenfakes"
	wfakes "github.com/concourse/atc/worker/workerfakes"

	"github.com/concourse/atc"
	. "github.com/concourse/atc/resource"
)

var _ = Describe("Resource In", func() {
	var (
		source  atc.Source
		params  atc.Params
		version atc.Version

		inScriptStdout     string
		inScriptStderr     string
		inScriptExitStatus int
		runInError         error

		inScriptProcess *gfakes.FakeProcess

		versionedSource VersionedSource

		ioConfig  IOConfig
		stdoutBuf *gbytes.Buffer
		stderrBuf *gbytes.Buffer

		fakeVolume *wfakes.FakeVolume

		signalsCh chan os.Signal
		readyCh   chan<- struct{}

		getErr error
	)

	BeforeEach(func() {
		source = atc.Source{"some": "source"}
		version = atc.Version{"some": "version"}
		params = atc.Params{"some": "params"}

		inScriptStdout = "{}"
		inScriptStderr = ""
		inScriptExitStatus = 0
		runInError = nil
		getErr = nil

		inScriptProcess = new(gfakes.FakeProcess)
		inScriptProcess.IDReturns("process-id")
		inScriptProcess.WaitStub = func() (int, error) {
			return inScriptExitStatus, nil
		}

		stdoutBuf = gbytes.NewBuffer()
		stderrBuf = gbytes.NewBuffer()

		ioConfig = IOConfig{
			Stdout: stdoutBuf,
			Stderr: stderrBuf,
		}

		fakeVolume = new(wfakes.FakeVolume)
		signalsCh = make(chan os.Signal)
		readyCh = make(chan<- struct{})
	})

	itCanStreamOut := func() {
		Describe("streaming bits out", func() {
			Context("when streaming out succeeds", func() {
				BeforeEach(func() {
					fakeVolume.StreamOutStub = func(path string) (io.ReadCloser, error) {
						streamOut := new(bytes.Buffer)

						if path == "some/subdir" {
							streamOut.WriteString("sup")
						}

						return ioutil.NopCloser(streamOut), nil
					}
				})

				It("returns the output stream of the resource directory", func() {
					inStream, err := versionedSource.StreamOut("some/subdir")
					Expect(err).NotTo(HaveOccurred())

					contents, err := ioutil.ReadAll(inStream)
					Expect(err).NotTo(HaveOccurred())
					Expect(string(contents)).To(Equal("sup"))
				})
			})

			Context("when streaming out fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					fakeVolume.StreamOutReturns(nil, disaster)
				})

				It("returns the error", func() {
					_, err := versionedSource.StreamOut("some/subdir")
					Expect(err).To(Equal(disaster))
				})
			})
		})
	}

	Describe("running", func() {
		JustBeforeEach(func() {
			fakeContainer.RunStub = func(spec garden.ProcessSpec, io garden.ProcessIO) (garden.Process, error) {
				if runInError != nil {
					return nil, runInError
				}

				_, err := io.Stdout.Write([]byte(inScriptStdout))
				Expect(err).NotTo(HaveOccurred())

				_, err = io.Stderr.Write([]byte(inScriptStderr))
				Expect(err).NotTo(HaveOccurred())

				return inScriptProcess, nil
			}

			fakeContainer.AttachStub = func(pid string, io garden.ProcessIO) (garden.Process, error) {
				if runInError != nil {
					return nil, runInError
				}

				_, err := io.Stdout.Write([]byte(inScriptStdout))
				Expect(err).NotTo(HaveOccurred())

				_, err = io.Stderr.Write([]byte(inScriptStderr))
				Expect(err).NotTo(HaveOccurred())

				return inScriptProcess, nil
			}

			versionedSource, getErr = resource.Get(fakeVolume, ioConfig, source, params, version, signalsCh, readyCh)
		})

		Context("when a result is already present on the container", func() {
			BeforeEach(func() {
				fakeContainer.PropertyStub = func(name string) (string, error) {
					switch name {
					case "concourse:resource-result":
						return `{
						"version": {"some": "new-version"},
						"metadata": [
							{"name": "a", "value":"a-value"},
							{"name": "b","value": "b-value"}
						]
					}`, nil
					default:
						return "", errors.New("unstubbed property: " + name)
					}
				}
			})

			It("exits successfully", func() {
				Expect(getErr).NotTo(HaveOccurred())
			})

			It("does not run or attach to anything", func() {
				Expect(fakeContainer.RunCallCount()).To(BeZero())
				Expect(fakeContainer.AttachCallCount()).To(BeZero())
			})

			It("can be accessed on the versioned source", func() {
				Expect(versionedSource.Version()).To(Equal(atc.Version{"some": "new-version"}))
				Expect(versionedSource.Metadata()).To(Equal([]atc.MetadataField{
					{Name: "a", Value: "a-value"},
					{Name: "b", Value: "b-value"},
				}))
			})
		})

		Context("when /in has already been spawned", func() {
			BeforeEach(func() {
				fakeContainer.PropertyStub = func(name string) (string, error) {
					switch name {
					case "concourse:resource-process":
						return "process-id", nil
					default:
						return "", errors.New("unstubbed property: " + name)
					}
				}
			})

			It("reattaches to it", func() {
				pid, io := fakeContainer.AttachArgsForCall(0)
				Expect(pid).To(Equal("process-id"))

				// send request on stdin in case process hasn't read it yet
				request, err := ioutil.ReadAll(io.Stdin)
				Expect(err).NotTo(HaveOccurred())

				Expect(request).To(MatchJSON(`{
					"source": {"some":"source"},
					"params": {"some":"params"},
					"version": {"some":"version"}
				}`))
			})

			It("does not run an additional process", func() {
				Expect(fakeContainer.RunCallCount()).To(BeZero())
			})

			Context("when /opt/resource/in prints the response", func() {
				BeforeEach(func() {
					inScriptStdout = `{
					"version": {"some": "new-version"},
					"metadata": [
						{"name": "a", "value":"a-value"},
						{"name": "b","value": "b-value"}
					]
				}`
				})

				It("can be accessed on the versioned source", func() {
					Expect(versionedSource.Version()).To(Equal(atc.Version{"some": "new-version"}))
					Expect(versionedSource.Metadata()).To(Equal([]atc.MetadataField{
						{Name: "a", Value: "a-value"},
						{Name: "b", Value: "b-value"},
					}))

				})

				It("saves it as a property on the container", func() {
					Expect(fakeContainer.SetPropertyCallCount()).To(Equal(1))

					name, value := fakeContainer.SetPropertyArgsForCall(0)
					Expect(name).To(Equal("concourse:resource-result"))
					Expect(value).To(Equal(inScriptStdout))
				})
			})

			Context("when /in outputs to stderr", func() {
				BeforeEach(func() {
					inScriptStderr = "some stderr data"
				})

				It("emits it to the log sink", func() {
					Expect(stderrBuf).To(gbytes.Say("some stderr data"))
				})
			})

			Context("when attaching to the process fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					runInError = disaster
				})

				It("returns an err", func() {
					Expect(getErr).To(HaveOccurred())
					Expect(getErr).To(Equal(disaster))
				})
			})

			Context("when the process exits nonzero", func() {
				BeforeEach(func() {
					inScriptExitStatus = 9
				})

				It("returns an err containing stdout/stderr of the process", func() {
					Expect(getErr).To(HaveOccurred())
					Expect(getErr.Error()).To(ContainSubstring("exit status 9"))
				})
			})

			itCanStreamOut()
		})

		Context("when /in has not yet been spawned", func() {
			BeforeEach(func() {
				fakeContainer.PropertyStub = func(name string) (string, error) {
					switch name {
					case "concourse:resource-process":
						return "", errors.New("nope")
					default:
						return "", errors.New("unstubbed property: " + name)
					}
				}
			})

			It("uses the same working directory for all actions", func() {
				err := versionedSource.StreamIn("a/path", &bytes.Buffer{})
				Expect(err).NotTo(HaveOccurred())

				Expect(fakeVolume.StreamInCallCount()).To(Equal(1))
				destPath, _ := fakeVolume.StreamInArgsForCall(0)

				_, err = versionedSource.StreamOut("a/path")
				Expect(err).NotTo(HaveOccurred())

				Expect(fakeVolume.StreamOutCallCount()).To(Equal(1))
				path := fakeVolume.StreamOutArgsForCall(0)
				Expect(path).To(Equal("a/path"))

				Expect(fakeContainer.RunCallCount()).To(Equal(1))

				Expect(destPath).To(Equal("/tmp/build/get/a/path"))
			})

			It("runs /opt/resource/in <destination> with the request on stdin", func() {
				spec, io := fakeContainer.RunArgsForCall(0)
				Expect(spec.Path).To(Equal("/opt/resource/in"))

				Expect(spec.Args).To(ConsistOf("/tmp/build/get"))

				request, err := ioutil.ReadAll(io.Stdin)
				Expect(err).NotTo(HaveOccurred())

				Expect(request).To(MatchJSON(`{
				"source": {"some":"source"},
				"params": {"some":"params"},
				"version": {"some":"version"}
			}`))
			})

			It("saves the process ID as a property", func() {
				Expect(fakeContainer.SetPropertyCallCount()).NotTo(BeZero())

				name, value := fakeContainer.SetPropertyArgsForCall(0)
				Expect(name).To(Equal("concourse:resource-process"))
				Expect(value).To(Equal("process-id"))
			})

			Context("when /opt/resource/in prints the response", func() {
				BeforeEach(func() {
					inScriptStdout = `{
					"version": {"some": "new-version"},
					"metadata": [
						{"name": "a", "value":"a-value"},
						{"name": "b","value": "b-value"}
					]
				}`
				})

				It("can be accessed on the versioned source", func() {
					Expect(versionedSource.Version()).To(Equal(atc.Version{"some": "new-version"}))
					Expect(versionedSource.Metadata()).To(Equal([]atc.MetadataField{
						{Name: "a", Value: "a-value"},
						{Name: "b", Value: "b-value"},
					}))

				})

				It("saves it as a property on the container", func() {
					Expect(fakeContainer.SetPropertyCallCount()).To(Equal(2))

					name, value := fakeContainer.SetPropertyArgsForCall(1)
					Expect(name).To(Equal("concourse:resource-result"))
					Expect(value).To(Equal(inScriptStdout))
				})
			})

			Context("when /in outputs to stderr", func() {
				BeforeEach(func() {
					inScriptStderr = "some stderr data"
				})

				It("emits it to the log sink", func() {
					Expect(stderrBuf).To(gbytes.Say("some stderr data"))
				})
			})

			Context("when running /opt/resource/in fails", func() {
				disaster := errors.New("oh no!")

				BeforeEach(func() {
					runInError = disaster
				})

				It("returns an err", func() {
					Expect(getErr).To(HaveOccurred())
					Expect(getErr).To(Equal(disaster))
				})
			})

			Context("when /opt/resource/in exits nonzero", func() {
				BeforeEach(func() {
					inScriptExitStatus = 9
				})

				It("returns an err containing stdout/stderr of the process", func() {
					Expect(getErr).To(HaveOccurred())
					Expect(getErr.Error()).To(ContainSubstring("exit status 9"))
				})
			})

			itCanStreamOut()
		})
	})

	Context("when a signal is received", func() {
		var waited chan<- struct{}
		var done chan struct{}

		BeforeEach(func() {
			fakeContainer.RunReturns(inScriptProcess, nil)
			fakeContainer.PropertyReturns("", errors.New("nope"))

			waiting := make(chan struct{})
			done = make(chan struct{})
			waited = waiting

			inScriptProcess.WaitStub = func() (int, error) {
				// cause waiting to block so that it can be aborted
				<-waiting
				return 0, nil
			}

			fakeContainer.StopStub = func(bool) error {
				close(waited)
				return nil
			}

			go func() {
				versionedSource, getErr = resource.Get(fakeVolume, ioConfig, source, params, version, signalsCh, readyCh)
				close(done)
			}()
		})

		It("stops the container", func() {
			signalsCh <- os.Interrupt
			<-done
			Expect(fakeContainer.StopCallCount()).To(Equal(1))
			Expect(fakeContainer.StopArgsForCall(0)).To(BeFalse())
		})

		It("doesn't send garden terminate signal to process", func() {
			signalsCh <- os.Interrupt
			<-done
			Expect(getErr).To(Equal(ErrAborted))
			Expect(inScriptProcess.SignalCallCount()).To(BeZero())
		})

		Context("when container.stop returns an error", func() {
			var disaster error

			BeforeEach(func() {
				disaster = errors.New("gotta get away")

				fakeContainer.StopStub = func(bool) error {
					close(waited)
					return disaster
				}
			})

			It("returns the error", func() {
				signalsCh <- os.Interrupt
				<-done
				Expect(getErr).To(Equal(ErrAborted))
			})
		})
	})
})
