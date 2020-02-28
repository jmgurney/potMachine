package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

var (
	xhyveIP string
)

func initializeXhyve(verbose bool) {
	potDirPath := getVagrantDirPath()
	xhyveDirPath := potDirPath + "/xhyve"

	if _, err := os.Stat(xhyveDirPath); os.IsNotExist(err) {
		fmt.Println("==> Creating ~/.pot/xhyve directory")
		os.Mkdir(xhyveDirPath, 0664)
	}

	//Download from github respo xhyve.tar.zg -> ~/.pot/xhyve
	fileURL := "https://app.vagrantup.com/ebarriosjr/boxes/FreeBSD12.1-zfs/versions/0.0.1/providers/xhyve.box"
	tarPath := xhyveDirPath + "/xhyve.tar.gz"

	fmt.Println("==> Checking if tar file already exists on ~/.pot/xhyve/xhyve.tar.gz")
	if _, err := os.Stat(tarPath); os.IsNotExist(err) {
		fmt.Println("==> Downloading tar file to ~/.pot/xhyve/xhyve.tar.gz")
		if err := downloadFile(tarPath, fileURL); err != nil {
			fmt.Println("Error downloading tar file from vagrant cloud with err: ", err)
			log.Fatal()
		}
	}

	fmt.Println("==> Extracting tar file ~/.pot/xhyve/xhyve.tar.gz to ~/.pot/xhyve/")
	//untar xhyve.tar.gz into ~/.pot/xhyve
	r, err := os.Open(tarPath)
	if err != nil {
		fmt.Println("Error openning tar file with err: ", err)
	}
	extractTarGz(r)

	fmt.Println("==> Cleaning up ~/.pot/xhyve/")
	// delete file
	err = os.Remove(xhyveDirPath + "/metadata.json")
	if err != nil {
		fmt.Println("Error removing files with err: ", err)
	}

	// delete file
	err = os.Remove(xhyveDirPath + "/initrd.gz")
	if err != nil {
		fmt.Println("Error removing files with err: ", err)
	}

	// delete file
	err = os.Remove(xhyveDirPath + "/vmlinuz")
	if err != nil {
		fmt.Println("Error removing files with err: ", err)
	}

	fmt.Println("==> Enabeling nfs mountpoint")
	//Enable NFS on mac sudo nfsd enable
	enableNFS()

	//GET uid of current user
	UUID := os.Getuid()

	//Edit NFS /etc/exports
	editNFSExports(UUID, potDirPath)

	//Check if runfile exists
	var runFile string
	if _, err := os.Stat(xhyveDirPath + "/runFreeBSD.sh"); os.IsNotExist(err) {
		//Create run file
		runFile = `#/bin/sh
UUID="-U potpotpo-potp-potp-potp-potmachinepp"
USERBOOT="~/.pot/xhyve/vmlinuz"
IMG="~/.pot/xhyve/block0.img"
KERNELENV=""

MEM="-m 4G"
SMP="-c 2"
PCI_DEV="-s 0:0,hostbridge -s 31,lpc"
NET="-s 2:0.virtio-net"
IMG_HDD="-s 4:0,virtio-blk,$IMG"
LPC_DEV="-l com1,stdio"
ACPI="-A"

xhyve $ACPI $MEM $SMP $PCI_DEV $LPC_DEV $NET $IMG_HDD $UUID -f fbsd,$USERBOOT,$IMG,"$KERNELENV"
`
		// Write runfile to ~/.pot/xhyve/runFreeBSD.sh
		xhyveRunFilePath := potDirPath + "/xhyve/runFreeBSD.sh"

		err = ioutil.WriteFile(xhyveRunFilePath, []byte(runFile), 0664)
		if err != nil {
			fmt.Println("ERROR: Error writting file to disk with err: \n", err)
			return
		}
	}
	//wg.Add()
	var wg sync.WaitGroup
	go netcat(&wg)

	//Initializa xhyve vm
	err = runXhyve()
	if err != nil {
		fmt.Println("Error creating xhyve vm with err: ", err)
		return
	}
	wg.Wait()

	//generate sshConfig file
	sshConfig := `Host potMachine
  HostName ` + xhyveIP + `
  User vagrant
  Port 22
  UserKnownHostsFile /dev/null
  StrictHostKeyChecking no
  PasswordAuthentication no
  IdentityFile ~/.pot/xhyve/private_key
  IdentitiesOnly yes
  LogLevel FATAL
`
	xhyvesshConfigFilePath := potDirPath + "/sshConfig"

	err = ioutil.WriteFile(xhyvesshConfigFilePath, []byte(sshConfig), 0664)
	if err != nil {
		fmt.Println("ERROR: Error writting file to disk with err: \n", err)
		return
	}
}

func runXhyve() error {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting user home path with err:", err)
		return err
	}
	var attr = os.ProcAttr{
		Dir: ".",
		Env: os.Environ(),
		Files: []*os.File{
			os.Stdin,
			nil,
			nil,
		},
	}
	process, err := os.StartProcess(home+"/.pot/xhyve/runFreeBSD.sh", []string{home + "/.pot/xhyve/runFreeBSD.sh"}, &attr)
	if err == nil {
		// It is not clear from docs, but Realease actually detaches the process
		err = process.Release()
		if err != nil {
			fmt.Println("Error releasing xhyve process with err: ", err.Error())
			return err
		}
	} else {
		fmt.Println("Error starting xhyve process with err: ", err.Error())
		return err
	}
	return nil
}

func editNFSExports(UUID int, potDir string) {
	//Probably gonna fail because of permission issues
	f, err := os.OpenFile("/etc/exports", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	textToAppend := `# POTMACHINE-Xhyve-Begin
` + potDir + ` -alldirs -mapall=` + string(UUID) + `
# POTMACHINE-Xhyve-END
`

	if _, err := f.WriteString(textToAppend); err != nil {
		log.Fatal("Can not write to /etc/exports file with err: ", err)
	}
}

func enableNFS() {
	termCmd := "sudo nfsd enable"
	cmd := exec.Command("bash", "-c", termCmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		fmt.Println("Error enabeling NFS with err: ", err)
		log.Fatal(err)
	}
	cmd.Wait()
}

func netcat(wg *sync.WaitGroup) {
	defer wg.Done()

	termCmd := "nc -l 1234"
	cmd := exec.Command("bash", "-c", termCmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		fmt.Println("Error getting ip information from the VM with err: ", err)
		log.Fatal(err)
	}
	cmd.Wait()
	xhyveIP = out.String()
}

func downloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

func extractTarGz(gzipStream io.Reader) {
	uncompressedStream, err := gzip.NewReader(gzipStream)
	if err != nil {
		log.Fatal("ExtractTarGz: NewReader failed")
	}

	tarReader := tar.NewReader(uncompressedStream)

	for true {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		}

		if err != nil {
			log.Fatalf("ExtractTarGz: Next() failed: %s", err.Error())
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(header.Name, 0755); err != nil {
				log.Fatalf("ExtractTarGz: Mkdir() failed: %s", err.Error())
			}
		case tar.TypeReg:
			outFile, err := os.Create(header.Name)
			if err != nil {
				log.Fatalf("ExtractTarGz: Create() failed: %s", err.Error())
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				log.Fatalf("ExtractTarGz: Copy() failed: %s", err.Error())
			}
			outFile.Close()

		default:
			log.Fatal("ExtractTarGz: uknown type:", header.Typeflag, " in ", header.Name)
		}

	}
}
