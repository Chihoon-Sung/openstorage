package nfs

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"

	log "github.com/Sirupsen/logrus"

	"github.com/libopenstorage/kvdb"
	"github.com/libopenstorage/openstorage/api"
	"github.com/libopenstorage/openstorage/volume"
)

const (
	Name     = "nfs"
	NfsDBKey = "OpenStorageNFSKey"
)

var (
	devMinor int32
)

// This data is persisted in a DB.
type awsVolume struct {
	spec      api.VolumeSpec
	formatted bool
	attached  bool
	mounted   bool
	device    string
	mountpath string
}

// Implements the open storage volume interface.
type nfsDriver struct {
	volume.DefaultBlockDriver
	db        kvdb.Kvdb
	nfsServer string
	mntPath   string
}

func Init(params volume.DriverParams) (volume.VolumeDriver, error) {
	uri, ok := params["uri"]
	if !ok {
		return nil, errors.New("No NFS server URI provided")
	}

	log.Println("NFS driver initializing with server: ", uri)

	out, err := exec.Command("uuidgen").Output()
	if err != nil {
		return nil, err
	}
	uuid := string(out)
	uuid = strings.TrimSuffix(uuid, "\n")

	inst := &nfsDriver{
		db:        kvdb.Instance(),
		mntPath:   "/mnt/" + uuid,
		nfsServer: uri}

	err = os.MkdirAll(inst.mntPath, 0744)
	if err != nil {
		return nil, err
	}

	log.Println("Binding NFS server to:", inst.mntPath)

	// Mount the nfs server locally on a unique path.
	err = syscall.Mount(inst.nfsServer, inst.mntPath, "tmpfs", 0, "mode=0700,uid=65534")
	if err != nil {
		os.Remove(inst.mntPath)
		return nil, err
	}

	log.Println("NFS initialized and driver mounted at: ", inst.mntPath)
	return inst, nil
}

func (d *nfsDriver) get(volumeID string) (*awsVolume, error) {
	v := &awsVolume{}
	key := NfsDBKey + "/" + volumeID
	_, err := d.db.GetVal(key, v)
	return v, err
}

func (d *nfsDriver) put(volumeID string, v *awsVolume) error {
	key := NfsDBKey + "/" + volumeID
	_, err := d.db.Put(key, v, 0)
	return err
}

func (d *nfsDriver) del(volumeID string) {
	key := NfsDBKey + "/" + volumeID
	d.db.Delete(key)
}

func (d *nfsDriver) String() string {
	return Name
}

func (d *nfsDriver) Create(l api.VolumeLocator, opt *api.CreateOptions, spec *api.VolumeSpec) (api.VolumeID, error) {
	out, err := exec.Command("uuidgen").Output()
	if err != nil {
		return "", err
	}
	volumeID := string(out)
	volumeID = strings.TrimSuffix(volumeID, "\n")

	// Create a directory on the NFS server with this UUID.
	err = os.MkdirAll(d.mntPath+volumeID, 0744)
	if err != nil {
		return "", err
	}

	// Persist the volume spec.  We use this for all subsequent operations on
	// this volume ID.
	err = d.put(volumeID, &awsVolume{device: d.mntPath + volumeID, spec: *spec})

	return api.VolumeID(volumeID), err
}

func (d *nfsDriver) Inspect(volumeIDs []api.VolumeID) ([]api.Volume, error) {
	return nil, nil
}

func (d *nfsDriver) Delete(volumeID api.VolumeID) error {
	v, err := d.get(string(volumeID))
	if err != nil {
		return err
	}

	// Delete the directory on the nfs server.
	err = os.Remove(v.device)
	if err != nil {
		return err
	}

	d.del(string(volumeID))

	return nil
}

func (d *nfsDriver) Snapshot(volumeID api.VolumeID, labels api.Labels) (api.SnapID, error) {
	return "", volume.ErrNotSupported
}

func (d *nfsDriver) SnapDelete(snapID api.SnapID) error {
	return volume.ErrNotSupported
}

func (d *nfsDriver) SnapInspect(snapID []api.SnapID) ([]api.VolumeSnap, error) {
	return []api.VolumeSnap{}, volume.ErrNotSupported
}

func (d *nfsDriver) Stats(volumeID api.VolumeID) (api.VolumeStats, error) {
	return api.VolumeStats{}, volume.ErrNotSupported
}

func (d *nfsDriver) Alerts(volumeID api.VolumeID) (api.VolumeAlerts, error) {
	return api.VolumeAlerts{}, volume.ErrNotSupported
}

func (d *nfsDriver) Enumerate(locator api.VolumeLocator, labels api.Labels) ([]api.Volume, error) {
	return []api.Volume{}, volume.ErrNotSupported
}

func (d *nfsDriver) SnapEnumerate(locator api.VolumeLocator, labels api.Labels) ([]api.VolumeSnap, error) {
	return nil, volume.ErrNotSupported
}

func (d *nfsDriver) Mount(volumeID api.VolumeID, mountpath string) error {
	v, err := d.get(string(volumeID))
	if err != nil {
		return err
	}

	err = syscall.Mount(v.device, mountpath, string(v.spec.Format), 0, "")
	if err != nil {
		return err
	}

	v.mountpath = mountpath
	v.mounted = true
	err = d.put(string(volumeID), v)

	return err
}

func (d *nfsDriver) Unmount(volumeID api.VolumeID, mountpath string) error {
	v, err := d.get(string(volumeID))
	if err != nil {
		return err
	}

	err = syscall.Unmount(v.mountpath, 0)
	if err != nil {
		return err
	}

	v.mountpath = ""
	v.mounted = false
	err = d.put(string(volumeID), v)

	return err
}

func (d *nfsDriver) Shutdown() {
	log.Printf("%s Shutting down", Name)
}

func init() {
	// Register ourselves as an openstorage volume driver.
	volume.Register(Name, volume.File, Init)
}
