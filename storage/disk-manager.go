package storage

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

const PageSize int64 = 4 * 1024

type DiskManagerV2 struct {
	DBdirectory string
	PageCatalog *Catalog
	FileCatalog *os.File
	TableObjs   map[TableName]*TableObj
}

type TableName string
type Catalog struct {
	Tables map[TableName]TableInfo
}

func NewDiskManagerV2(dbDirectory string) (*DiskManagerV2, error) {
	var manager DiskManagerV2

	if _, err := os.Stat(dbDirectory); err != nil && os.IsNotExist(err) {
		manager, err = CreatDefaultManager(dbDirectory)
		if err != nil {
			return nil, fmt.Errorf("NewDiskManagerV2: %w", err)
		}
		log.Println("Created Default Manager")
	} else {
		manager, err = ReadExistingManager(dbDirectory)
		if err != nil {
			return nil, fmt.Errorf("NewDiskManagerV2: %w", err)
		}
		log.Println("Loaded Existing Manager")
	}
	return &manager, nil
}

func CreatDefaultManager(dbDirectory string) (DiskManagerV2, error) {
	err := os.Mkdir(dbDirectory, 0755)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("CreatDefaultManager (create db Dir error): %w", err)
	}

	err = os.Mkdir(dbDirectory+"/Tables", 0755)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("CreatDefaultManager (create table dir error): %w", err)
	}

	catalogFilePtr, err := os.Create(dbDirectory + "/catalog")
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("CreatDefaultManager (create catalog file error): %w", err)
	}

	catalog := Catalog{Tables: make(map[TableName]TableInfo)}
	encodedCatalog, err := SerializeCatalog(&catalog)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("CreatDefaultManager: %w", err)
	}

	_, err = catalogFilePtr.Write(encodedCatalog)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("CreatDefaultManager (catalog writing error): %w", err)
	}

	dm := DiskManagerV2{
		DBdirectory: dbDirectory,
		PageCatalog: &catalog,
		FileCatalog: catalogFilePtr,
		TableObjs:   make(map[TableName]*TableObj),
	}

	return dm, nil
}

func ReadExistingManager(dbDirectory string) (DiskManagerV2, error) {
	catalogPath := filepath.Join(dbDirectory, "catalog")

	file, err := os.OpenFile(catalogPath, os.O_RDWR|os.O_CREATE, 0777)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("ReadExistingManager: %w", err)
	}

	bytes, err := ReadNonPageFile(file)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("ReadExistingManager: %w", err)
	}

	catalog, err := DeserializeCatalog(bytes)
	if err != nil {
		return DiskManagerV2{}, fmt.Errorf("ReadExistingManager: %w", err)
	}

	dm := DiskManagerV2{
		DBdirectory: dbDirectory,
		PageCatalog: catalog,
		FileCatalog: file,
		TableObjs:   make(map[TableName]*TableObj),
	}

	return dm, nil
}

func (dm *DiskManagerV2) WriteToDisk(page *PageV2) error {
	tableInfo := dm.TableObjs[TableName(page.TABLE)]
	pageObj, found := tableInfo.DirectoryPage.Value[PageID(page.Header.ID)]

	if !found {
		pageOffset, err := dm.WritePageEOFV2(page, tableInfo.DataFile)
		if err != nil {
			return fmt.Errorf("WriteToDisk: %w", err)
		}

		pageInfo := PageInfo{
			Offset:       pageOffset,
			PointerArray: page.PointerArray,
		}

		tableInfo.DirectoryPage.Value[PageID(page.Header.ID)] = &pageInfo

		err = dm.UpdateDirectoryPageDisk(tableInfo.DirectoryPage, tableInfo.DirFile)
		if err != nil {
			return fmt.Errorf("WriteToDisk: %w", err)
		}
	} else {
		err := dm.WritePageBackV2(page, pageObj.Offset, tableInfo.DataFile)
		if err != nil {
			return fmt.Errorf("WriteToDisk: %w", err)
		}
	}

	return nil
}

func (dm *DiskManagerV2) UpdateDirectoryPageDisk(page *DirectoryPageV2, dirFile *os.File) error {
	pageBytes, err := EncodeDirectory(page)
	if err != nil {
		return fmt.Errorf("UpdateDirectoryPageDisk: %w", err)
	}

	_, err = dirFile.WriteAt(pageBytes, 0)
	if err != nil {
		return fmt.Errorf("UpdateDirectoryPageDisk (Writing to Dir File): %w", err)
	}

	return nil
}

func FindAvailablePage(tablePtr *os.File, bytesNeeded int) (*PageV2, error) {
	var offset int64
	var page *PageV2

	for {
		pageBytes := make([]byte, PageSize)
		_, err := tablePtr.ReadAt(pageBytes, int64(offset))
		if err != nil {
			if err == io.EOF {
				fmt.Println("FindAvailablePage (End of file reached, creating new page)")
				return CreatePageV2(), nil
			}

			return nil, fmt.Errorf("FindAvailablePage(erro reading from file non-EOF): %w", err)
		}

		offset += PageSize
		foundPage, err := DecodePageV2(pageBytes)
		if err != nil {
			return nil, fmt.Errorf("FindAvailablePage: %w", err)
		}

		canInsert := foundPage.Header.UpperPtr-foundPage.Header.LowerPtr >= uint16(bytesNeeded)
		if canInsert {
			page = foundPage
			break
		}
	}

	return page, nil
}
