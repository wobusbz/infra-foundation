package model

func Register(model Model) error {
	return DefaultModelManager.Register(model)
}
