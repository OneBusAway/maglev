package restapi
        
func collectAllNestedIdsFromObjects(list []interface{}, key string) (ids []string) {
	for _, object := range list {
		object, _ := object.(map[string]interface{})
		objectValue := object[key].([]interface{})
		for _, id := range objectValue {
			ids = append(ids, id.(string))
		}
	}
	return ids
}

func collectAllIdsFromObjects(list []interface{}, key string) (ids []string) {
	for _, object := range list {
		object, _ := object.(map[string]interface{})
		ids = append(ids, object[key].(string))
	}
	return ids
}
